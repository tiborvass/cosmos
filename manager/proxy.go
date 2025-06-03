package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/tiborvass/cosmos/manager/httputil"
	. "github.com/tiborvass/cosmos/utils"
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

func startProxy(addr string) *http.Server {
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

	s := &http.Server{Addr: addr, Handler: proxy}
	go func() {
		M(s.ListenAndServe())
	}()
	return s
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos-manager <coding-agent>")
	fmt.Fprintln(os.Stderr, "coding-agent: only \"claude\" is currently supported")
}

func main() {
	fmt.Println("hi from proxy")
	defer fmt.Println("bye from proxy")
	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer func() {
		if x := recover(); x != nil {
			fmt.Fprintln(os.Stderr, x)
		}
		stop()
	}()

	cleanups := []func(){}

	go func() {
		<-ctx.Done()
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	codingAgent := os.Args[1]
	if codingAgent != "claude" {
		usage()
		os.Exit(1)
	}

	addr := ":8042"
	l := M2(net.Listen("tcp", addr))
	var (
		conn net.Conn
		err  error
	)
	fmt.Println("connecting to client")
	maxRetries := 5
	backoff := time.Second / 2
	for range maxRetries {
		conn, err = l.Accept()
		if err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "unable to listen on %s: %v, retrying in %v...\n", addr, err, backoff)
		time.Sleep(backoff)
		backoff *= 2
	}
	fmt.Println("connected to client")

	cleanups = append(cleanups, func() {
		if conn != nil {
			conn.Close()
		}
	})

	proxy := startProxy(":8080")

	cleanups = append(cleanups, func() {
		fmt.Println("shutting down server")
		M(proxy.Shutdown(context.Background()))
	})

	fmt.Println("proxy started")
	agentID := R(ctx, "docker run -dit --rm --net container:%s -e ANTHROPIC_BASE_URL=http://localhost:8080 -v /tmp/claude-credentials.json:/root/.claude/.credentials.json cosmos-agent:claude", M2(os.Hostname()))
	fmt.Println(agentID)
	enc := json.NewEncoder(conn)
	M(enc.Encode(agentID))
	R(ctx, "docker wait %s", agentID)
	time.Sleep(time.Second * 30)
}
