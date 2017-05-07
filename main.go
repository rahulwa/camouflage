package main

import (
	"flag"
	"time"

	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/garyburd/redigo/redis"
	"github.com/golang/glog"
	"github.com/rahulwa/camouflage/proxy"
)

func init() {
	proxy.Conf = &proxy.Config{
		RedisControllerChannel: "proxiesChannel",
		RedisProxiesList:       "proxiesList",
		ProxyPort:              8080,
		MetricPort:             9000,
		Verbose:                false,
		DumpInWireFormat:       false,
	}
	flag.Parse()
}

func main() {
	c, e := redis.Dial("tcp", "127.0.0.1:6379")
	if e != nil {
		glog.Fatal("redis: unable to connect")
	}
	logger, _ := fluent.New(fluent.Config{TagPrefix: "debug.test"})
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
				glog.Fatalf("redis: unable to connect %s: %v", addr, err)
			}
			if _, err := c.Do("SELECT", db); err != nil {
				glog.Fatalf("redis: unable to select db %d: %v", db, err)
			}
			return c, err
		},
	}
}
