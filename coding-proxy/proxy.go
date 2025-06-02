package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/tiborvass/cosmos/coding-proxy/httputil"
)

var numRequests int

/*
type debugTransport struct {
	sync.Mutex
}

func (t *debugTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Make requests sequential for now to make it easier to debug.
	t.Lock()
	defer t.Unlock()
	numRequests++

	reqDump, err := httputil.DumpRequestOut(r, true)
	if err != nil {
		return nil, err
	}
	log.Printf("===[REQUEST %d]===\n\n%s\n\n", numRequests, reqDump)

	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	bodyReader := func(r io.ReadCloser) (io.ReadCloser, error) {
		switch ce := resp.Header.Get("Content-Encoding"); ce {
		case "br":
			return io.NopCloser(brotli.NewReader(r)), nil
		case "gzip":
			return gzip.NewReader(r)
		case "":
			return r, nil
		default:
			return nil, fmt.Errorf("unhandled Content-Encoding %s", ce)
		}
	}

	respDump, err := httputil.DumpResponse(resp, true, bodyReader)
	if err != nil {
		return nil, err
	}

	log.Printf("===[RESPONSE %d]===\n\n%s\n\n", numRequests, respDump)
return resp, nil
}
*/

func startProxy(addr string) {
	log.Printf("Proxy listening on %s\n", addr)

	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()
			fmt.Printf("inp: %+v\n", pr.In)
			fmt.Println()
			fmt.Printf("out: %+v\n", pr.Out)
			fmt.Println()
			fmt.Println()
		},
		ModifyResponse: func(resp *http.Response) error {
			defer m.Unlock()
			bodyReader := func(r io.ReadCloser) (io.ReadCloser, error) {
				switch ce := resp.Header.Get("Content-Encoding"); ce {
				case "br":
					return io.NopCloser(brotli.NewReader(r)), nil
				case "gzip":
					return gzip.NewReader(r)
				case "":
					return r, nil
				default:
					return nil, fmt.Errorf("unhandled Content-Encoding %s", ce)
				}
			}
			respDump, err := httputil.DumpResponse(resp, true, bodyReader)
			if err != nil {
				return err
			}

			log.Printf("===[RESPONSE %d]===\n\n%s\n\n", numRequests, respDump)
			return nil
		},
	}

	// proxy.Transport = new(debugTransport)
	// d := proxy.Director
	// proxy.Director = func(r *http.Request) {
	// 	d(r) // call default director
	// 	r.Host = target.Host // set Host header as expected by target
	// }

	if err := http.ListenAndServe(addr, proxy); err != nil {
		panic(err)
	}
}

func main() {
	startProxy(":8080")
}
