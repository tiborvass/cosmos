package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
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

const (
	ANTHROPIC_BASE_DOMAIN = "api.anthropic.com"
)

var numRequests = 1

// Handle CONNECT requests (HTTPS tunneling)
// func handleConnect(w http.ResponseWriter, r *http.Request) {
// 	// "Hijack" the connection
// 	h, ok := w.(http.Hijacker)
// 	if !ok {
// 		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
// 		return
// 	}
// 	clientConn, bufrw, err := h.Hijack()
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusServiceUnavailable)
// 		return
// 	}
// 	// Connect to the destination server
// 	destConn, err := net.Dial("tcp", r.Host)
// 	if err != nil {
// 		bufrw.WriteString("HTTP/1.1 502 Bad Gateway\r\n\r\n")
// 		bufrw.Flush()
// 		clientConn.Close()
// 		return
// 	}
// 	bufrw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
// 	bufrw.Flush()
// 	// Tunnel: copy bytes both ways
// 	go io.Copy(destConn, clientConn)
// 	go io.Copy(clientConn, destConn)
// }

func startProxy(addr string) *http.Server {
	log.Printf("Proxy listening on %s\n", addr)

	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()
			// fmt.Printf("inp: %+v\n", pr.In)
			// fmt.Println()
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			reqDump := M2(httputil.DumpRequestOut(pr.Out, true))
			log.Printf("===[REQUEST %d]===\n\n%s\n\n", numRequests, reqDump)
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

	s := &http.Server{Addr: addr}

	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			w.WriteHeader(http.StatusBadGateway)
			s.Shutdown(context.Background())
			// handleConnect(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}

	s.Handler = http.HandlerFunc(handler)

	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
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
		if err := proxy.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	})

	fmt.Println("proxy started")
	agentID := R(ctx, "docker run --init -dit --rm --net container:%s -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 -e ANTHROPIC_BASE_URL=http://localhost:8080 -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -v /tmp/claude.json:/root/.claude.json -w /root/vibing cosmos-agent:claude", M2(os.Hostname()))
	fmt.Println(agentID)
	enc := json.NewEncoder(conn)
	M(enc.Encode(agentID))
	fmt.Println("waiting")
	R(ctx, "docker wait %s", agentID)
}
