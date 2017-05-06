package main

import (
	"flag"
	"log"
	"net/url"

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
	proxy.Proxy(nil, nil)
}
