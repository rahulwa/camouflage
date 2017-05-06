package main

import (
	"flag"
	"log"
	"net/url"
	"time"

	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/garyburd/redigo/redis"
	"github.com/golang/glog"
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
	logger, e := fluent.New(fluent.Config{TagPrefix: "watch-tower", FluentPort: 8000, FluentNetwork: "tcp", MarshalAsJSON: true})
	if e != nil {
		glog.Fatalf("main: fluent error: %q", e)
	}
	flag.PrintDefaults()
	pool := newPool("127.0.0.1:6379", 0)
	proxy.Proxy(pool, logger)
}

func newPool(addr string, db int) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     100, // Max Idle connections
		MaxActive:   0,   // max number of connections, 0: Unlimited (assuming localy available)
		IdleTimeout: 600 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", addr)
			if err != nil {
				glog.Fatalf("main: redis: unable to connect %s: %v", addr, err)
			}
			if _, err := c.Do("SELECT", db); err != nil {
				glog.Fatalf("main: redis: unable to select db %d: %v", db, err)
			}
			return c, err
		},
	}
}
