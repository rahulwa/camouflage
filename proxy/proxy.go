// A HTTP proxy package for intercepting and modifying traffic and forwards requests through next level of proxies servers.
// It Does roundrobin based on per Request.Host, not blindly.
// This package is extended upon github.com/elazarl/goproxy. Full functionality of it can access throgh GoProxy variable,
// But ctx.UserData must not be altered. And glog library for logging.
// It uses redis list for saving next level of proxies's address.
// It publishes Request.Host, sender-proxy and retured status-code for bad status on redis Channel.
// It uses fluentd for dumping http request and response in json or raw wire format.
// And Exposes rudimentary metrics via prometheus client library.
package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"sync"

	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/garyburd/redigo/redis"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rahulwa/goproxy"
	"github.com/satori/go.uuid"
)

type Config struct {
	// Redis channel to be published json message containing Request.Host, sender-proxy and retured statuscode for bad status (400 <= StatusCode <= 599).
	RedisControllerChannel string

	// Redis List containing next level of proxies address. It must not be non-empty list.
	// All address must be in scheme://[user:password@]host:port form and must not be empty.
	RedisProxiesList string

	// Running proxy on this port and must not be empty.
	ProxyPort int

	// Exposing metrics on this port and must not be empty.
	MetricPort int

	// Enabling verbosabilty on underlying goproxy library.
	Verbose bool

	// If true, Dumps http request and response in wire format else in human readable format.
	DumpInWireFormat bool
}

type siteProxyPointer struct {
	sync.RWMutex
	m map[string]int
}

type controllerMessage struct {
	Site       string
	Sender     string
	StatusCode int
}

var (
	// New goproxy server. Functionality of goproxy can access throgh this variable but ctx.UserData must not be altered.
	GoProxy = goproxy.NewProxyHttpServer()

	// Instance of Config struct
	Conf = new(Config)

	proxyPointer = siteProxyPointer{m: make(map[string]int)}

	// Prometheus NewCounterVec
	ToatlProxyRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "Proxy_Requests_Total",
			Help: "Total Number of Request per Response StatusCode and sender.",
		},
		[]string{"site", "statusCode", "sender"},
	)
)

func init() {
	prometheus.MustRegister(ToatlProxyRequests)
}

// Rudimentary RedisProxiesList Check.
func proxiesListCheck(redispool *redis.Pool) {
	c := redispool.Get()
	defer c.Close()
	if l, err := redis.Int(c.Do("LLEN", Conf.RedisProxiesList)); err != nil || l == 0 {
		glog.Fatalf("proxy: redis: 'proxiesList' is an invalid list: ", err)
	}
}

// Implements simple roundrobin per site.
func roundRobin(redispool *redis.Pool, site string) string {
	c := redispool.Get()
	defer c.Close()
	proxyPointer.Lock()
	defer proxyPointer.Unlock()
	l, _ := redis.Int(c.Do("LLEN", Conf.RedisProxiesList))
	currentProxy, ok := proxyPointer.m[site]
	if !ok || currentProxy >= l {
		proxyPointer.m[site] = 0
		currentProxy = 0
	}
	currentProxyAddress, _ := redis.String(c.Do("LINDEX", Conf.RedisProxiesList, currentProxy))
	glog.V(1).Infof("proxy: rounrobin: currentProxy is %d:%s", currentProxy, currentProxyAddress)
	proxyPointer.m[site]++
	return currentProxyAddress
}

// Impletments proxy functionality.
// If fluent is nil, then doesn't dump http request and response.
// It adds a uuid to request, to bind it to corresponding response.
func Proxy(redispool *redis.Pool, fluent *fluent.Fluent) {
	defer redispool.Close()
	defer glog.Flush()
	glog.Info("starting proxy server...")
	glog.Infof("using RedisProxiesList: %q RedisControllerChannel: %q", Conf.RedisProxiesList, Conf.RedisControllerChannel)
	GoProxy.Verbose = Conf.Verbose
	proxiesListCheck(redispool)
	//This function "onRequstCommonFunc" is being executed for each http[s] requests.
	//Doing roundrobin for next line of proxies
	onRequstCommonFunc := func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		senderProxy := roundRobin(redispool, r.Host)
		id := uuid.NewV4()
		uuid := id.Bytes()
		ctx.UserData = map[string][]byte{"sender": []byte(senderProxy), "uuid": uuid}
		GoProxy.Tr = &http.Transport{Dial: forward(senderProxy, GoProxy)}
		if fluent != nil {
			rq, e := httputil.DumpRequest(r, true)
			if e != nil {
				glog.Errorf("proxy: httpmessagedump: onrequest: %q", e)
				return r, nil
			}
			glog.V(3).Infof("proxy: httpmessagedump: onrequest: %q", rq)
			go dumphttpRequest(uuid, fluent, rq)
		}
		return r, nil
	}
	//Running on each Request
	GoProxy.OnRequest().DoFunc(onRequstCommonFunc)
	//Doing MiTM http connect Requests
	GoProxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*:80$"))).
		HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			return goproxy.HTTPMitmNewConnect, host
		})
	//Doing MiTM https [connect] Requests
	GoProxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*:443$"))).
		HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			return goproxy.MitmConnect, host
		})
	//Running on each Response
	GoProxy.OnResponse().DoFunc(func(r *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		v, ok := ctx.UserData.(map[string][]byte)
		if !ok {
			glog.Error("proxy: proxy: onresponse: invalid userdata")
			return r
		}
		sender := string(v["sender"])
		glog.Infof("%s => %s[%d]", sender, r.Request.Host, r.StatusCode)
		ToatlProxyRequests.With(prometheus.Labels{"site": r.Request.Host, "statusCode": fmt.Sprintf("%d", r.StatusCode), "sender": sender}).Inc()
		if r.StatusCode >= 400 && r.StatusCode <= 599 {
			c := redispool.Get()
			defer c.Close()
			msg := &controllerMessage{
				Site:       r.Request.Host,
				Sender:     sender,
				StatusCode: r.StatusCode,
			}
			tmp, e := json.Marshal(*msg)
			if e != nil {
				glog.Errorf("proxy: proxy: onresponse: json marshal error: %q", e)
				return r
			}
			c.Do("PUBLISH", Conf.RedisControllerChannel, tmp)
			c.Flush()
		}
		if fluent != nil {
			uuid := v["uuid"]
			rsp, e := httputil.DumpResponse(r, true)
			if e != nil {
				glog.Errorf("proxy: httpmessagedump: onresponse: %q", e)
				return r
			}
			glog.V(5).Infof("proxy: httpmessagedump: onresponse: %q", rsp)
			go dumphttpResponse(uuid, fluent, rsp)
		}
		return r
	})
	//The Handler function provides a default handler to expose metrics to Prometheus Server
	serverMuxA := http.NewServeMux()
	serverMuxA.Handle("/", promhttp.Handler())
	//Listening for Prometheus Server
	go func() {
		glog.Info("starting proxy metric server...")
		glog.Error(http.ListenAndServe(fmt.Sprintf(":%d", Conf.MetricPort), serverMuxA))
	}()
	//Listening for proxy server
	glog.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", Conf.ProxyPort), GoProxy))
}

// Forwarder to next level of proxies.
func forward(senderProxy string, GoProxy *goproxy.ProxyHttpServer) func(network, addr string) (net.Conn, error) {
	u, err := url.Parse(senderProxy)
	if err != nil {
		glog.Errorf("proxy: forward: failed to parse senderProxy: '%s': %q", senderProxy, err)
	}
	return func(network, addr string) (net.Conn, error) {
		// Prevent proxy server from being re-directed
		if u.Host == addr {
			return net.Dial(network, addr)
		}
		glog.V(1).Infof("proxy: forward: tr...dial %s => ", addr)
		dialer := GoProxy.NewConnectDialToProxy(senderProxy)
		if dialer == nil {
			glog.Error("proxy: forward: dialer: nil dialer, invalid uri?")
		}
		return dialer(network, addr)
	}
}
