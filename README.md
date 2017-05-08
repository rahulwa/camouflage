# camouflage

An HTTP proxy package for intercepting, modifying traffic and forwarding it via a reverse proxy server (like HAproxy).

This package is extended upon [elazarl's goproxy](https://github.com/elazarl/goproxy). Full functionality of it can be access through `GoProxy` variable, But `ctx.UserData` must not be altered. 

```go
package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"

	"github.com/elazarl/goproxy"
	"github.com/rahulwa/camouflage/proxy"
)

func init() {
	rProxyServer, err := url.Parse("http://127.0.0.1:8081")
	if err != nil {
		log.Fatal("main: init: unable to parse reverseproxyadress")
	}
	proxy.Conf = &proxy.Config{
		RedisControllerChannel: "proxiesChannel",
		ReverseProxyServer:     *rProxyServer,
		ProxyPort:              8080,
		MetricPort:             9099,
		Verbose:                false,
		DumpInWireFormat:       false,
	}
	flag.Parse()
}

func main() {
  // This line will add X-GoProxy: yxorPoG-X header to all requests sent through the proxy. 
  proxy.GoProxy.OnRequest().DoFunc(
    func(r *http.Request,ctx *goproxy.ProxyCtx)(*http.Request,*http.Response) {
        r.Header.Set("X-GoProxy","yxorPoG-X")
        return r,nil
  })
  // Start the proxy server 
  proxy.Proxy(nil, nil)
}
```

For more example, see [examples](https://github.com/rahulwa/camouflage/tree/master/examples/main) directory and [documentation](https://godoc.org/github.com/rahulwa/camouflage/proxy).

## Features
- supports regular HTTP proxy, HTTPS through CONNECT, and "hijacking" HTTPS connection using "Man in the Middle" style attack (thanks to goproxy).
- requests and responses are fully customizable (thanks to goproxy).
- proxy chaining with the help of any reverse proxy.
- save/send requests and responses to nearly anywhere with the help of [fluentd](http://www.fluentd.org/). 
- metrics exposures via [prometheus-client](https://github.com/prometheus/client_golang/).
- publishes json on the configured redis Channel - containing the destination site, reverse-proxy-server and status-code for each bad response happens from destination-site with the status code between 400 to 599 for real-time analysis or action.
