// An HTTP proxy package for intercepting, modifying traffic and forwarding it via a reverse proxy server (like HAproxy).
//
// This package is extended upon github.com/elazarl/goproxy. Full functionality of it can access through GoProxy variable,
// But ctx.UserData must not be altered. And glog library for logging.
//
// It publishes Request.Host, reverse-proxy-server and retured status-code for bad status on a redis Channel.
// It uses fluentd for dumping http requests and responses in json or raw wire format.
// And Exposes rudimentary metrics via Prometheus client library.
package proxy

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/garyburd/redigo/redis"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	gouuid "github.com/satori/go.uuid"
)

type Config struct {
	// Redis channel to be published json message containing Request.Host, sender-proxy and retured statuscode for bad status (400 <= StatusCode <= 599).
	RedisControllerChannel string

	// Forward traffic to it (only HTTP Scheme is supported) and must not be empty.
	ReverseProxyServer url.URL

	// Running proxy on this port and must not be empty.
	ProxyPort int

	// Exposing metrics on this port and must not be empty.
	MetricPort int

	// Enabling verbosabilty on underlying goproxy library.
	Verbose bool

	// If true, Dumps http request and response in wire format else in human readable format.
	DumpInWireFormat bool
}

type controllerMessage struct {
	Site        string
	SenderProxy string
	StatusCode  int
}

var (
	// New goproxy server. Functionality of goproxy can access throgh this variable but ctx.UserData must not be altered.
	GoProxy = goproxy.NewProxyHttpServer()

	// Instance of Config struct
	Conf = new(Config)

	// Prometheus NewCounterVec
	toatlProxyRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_requests_total",
			Help: "Total Number of Request per Response StatusCode and site.",
		},
		[]string{"site", "statusCode"},
	)
)

func init() {
	prometheus.MustRegister(toatlProxyRequests)
}

// Impletments proxy functionality.
// If fluent is nil, then doesn't dump http request and response.
// If redispool is nil, then doesn't publish on redis Channel for bad status.
// It adds a uuid to request, to bind it to corresponding response and send it to client as a header on response.
func Proxy(redispool *redis.Pool, fluent *fluent.Fluent) {
	defer redispool.Close()
	defer glog.Flush()
	glog.Info("starting proxy server...")
	glog.Infof("using ReverseProxyServer: %q, RedisControllerChannel: %q", Conf.ReverseProxyServer.String(), Conf.RedisControllerChannel)
	GoProxy.Verbose = Conf.Verbose

	//This function "onRequstCommonFunc" is being executed for each http[s] requests.
	onRequstCommonFunc := func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		id := gouuid.NewV4()
		uuid := id.Bytes()
		ctx.UserData = map[string][]byte{"uuid": uuid}
		GoProxy.Tr = &http.Transport{Dial: forward(Conf.ReverseProxyServer, GoProxy)}
		if fluent != nil {
			rq, e := httputil.DumpRequest(r, true)
			if e != nil {
				glog.Errorf("proxy: httpmessagedump: onrequest: %q", e)
				return r, nil
			}
			glog.V(5).Infof("proxy: httpmessagedump: onrequest: %q", rq)
			go dumphttpRequest(uuid, fluent, rq)
		}
		return r, nil
	}
	//Running on each Request
	GoProxy.OnRequest().DoFunc(onRequstCommonFunc)
	//Doing MiTM http connect Requests
	GoProxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile("^.*:80$"))).
		HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			return goproxy.HTTPMitmConnect, host
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
		glog.Infof("%s => %s[%d]", Conf.ReverseProxyServer.String(), r.Request.Host, r.StatusCode)
		toatlProxyRequests.With(prometheus.Labels{"site": r.Request.Host, "statusCode": fmt.Sprintf("%d", r.StatusCode)}).Inc()
		if redispool != nil && r.StatusCode >= 400 && r.StatusCode <= 599 {
			c := redispool.Get()
			defer c.Close()
			msg := &controllerMessage{
				Site:        r.Request.Host,
				SenderProxy: Conf.ReverseProxyServer.String(),
				StatusCode:  r.StatusCode,
			}
			tmp, e := json.Marshal(*msg)
			if e != nil {
				glog.Errorf("proxy: proxy: onresponse: json marshal error: %q", e)
				return r
			}
			c.Do("PUBLISH", Conf.RedisControllerChannel, tmp)
			c.Flush()
		}
		uuid := v["uuid"]
		if fluent != nil {
			rsp, e := httputil.DumpResponse(r, true)
			if e != nil {
				glog.Errorf("proxy: httpmessagedump: onresponse: %q", e)
				return r
			}
			glog.V(5).Infof("proxy: httpmessagedump: onresponse: %q", rsp)
			go dumphttpResponse(uuid, fluent, rsp)
		}
		id, err := gouuid.FromBytes(uuid)
		if err != nil {
			glog.Error("proxy: proxy: unable to parse uuid")
			return r
		}
		r.Header.Set("X-Camouflage-Context", id.String())
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

// Forwarder to reverse proxy server.
func forward(u url.URL, GoProxy *goproxy.ProxyHttpServer) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		// Prevent proxy server from being re-directed
		if u.Host == addr {
			return net.Dial(network, addr)
		}
		glog.V(1).Infof("proxy: forward: tr...dial %s => ", addr)
		dialer := newConnectDialToProxyWithAuth(GoProxy, u)
		if dialer == nil {
			glog.Error("proxy: forward: dialer: nil dialer, invalid uri?")
		}
		return dialer(network, addr)
	}
}

func newConnectDialToProxyWithAuth(proxy *goproxy.ProxyHttpServer, u url.URL) func(network, addr string) (net.Conn, error) {
	if u.Scheme == "" || u.Scheme == "http" {
		if strings.IndexRune(u.Host, ':') == -1 {
			u.Host += ":80"
		}
		return func(network, addr string) (net.Conn, error) {
			connectReq := &http.Request{
				Method: "CONNECT",
				URL:    &url.URL{Opaque: addr},
				Host:   addr,
				Header: make(http.Header),
				Close:  true,
			}
			// Adding auth header if user:password is present in url
			if u.User != nil {
				basic := "Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.String()))
				connectReq.Header.Add("Proxy-Authorization", basic)
			}
			c, err := dialWithAuth(proxy, network, u.Host)
			if err != nil {
				return nil, err
			}
			connectReq.Write(c)
			// Read response.
			// Okay to use and discard buffered reader here, because
			// TLS server will not speak until spoken to.
			br := bufio.NewReader(c)
			resp, err := http.ReadResponse(br, connectReq)
			if err != nil {
				c.Close()
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				resp, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				c.Close()
				return nil, errors.New("proxy refused connection" + string(resp))
			}
			return c, nil
		}
	}
	return nil
}

func dialWithAuth(proxy *goproxy.ProxyHttpServer, network, addr string) (c net.Conn, err error) {
	if proxy.Tr.Dial != nil {
		return proxy.Tr.Dial(network, addr)
	}
	return net.Dial(network, addr)
}
