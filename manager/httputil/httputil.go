package httputil

import (
	"net/http/httputil"
	"net/url"
)

type ReverseProxy = httputil.ReverseProxy

type ProxyRequest = httputil.ProxyRequest

func NewSingleHostReverseProxy(target *url.URL) *ReverseProxy {
	return httputil.NewSingleHostReverseProxy(target)
}
