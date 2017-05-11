# camouflage
An HTTP proxy server package

A HTTP proxy package for intercepting and modifying traffic and forwards requests through next level of proxies servers.

It Does roundrobin based on per Request.Host, not blindly.

This package is extended upon github.com/elazarl/goproxy. Full functionality of it can access throgh GoProxy variable, But ctx.UserData must not be altered. And glog library for logging.

It uses redis list for saving next level of proxies's address.

It publishes Request.Host, sender-proxy and retured status-code for bad status on redis Channel.

It uses fluentd for dumping http request and response in json or raw wire format. And Exposes rudimentary metrics via prometheus client library.
