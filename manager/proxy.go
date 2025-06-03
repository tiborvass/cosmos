package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/r3labs/sse"
	"github.com/tiborvass/cosmos/ctxio"
	. "github.com/tiborvass/cosmos/utils"
)

const (
	ANTHROPIC_BASE_DOMAIN = "api.anthropic.com"
)

var numRequests = 0

func startProxy(addr string) *http.Server {
	log.Printf("Proxy listening on %s\n", addr)

	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// One request at a time
			// Unlock is in ModifyResponse
			m.Lock()
			numRequests++
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			pr.Out.Host = ANTHROPIC_BASE_DOMAIN
			reqDump := M2(httputil.DumpRequestOut(pr.Out, true))
			log.Printf("=== [REQUEST %d] ===\n\n%s\n\n", numRequests, reqDump)
		},
		ModifyResponse: func(resp *http.Response) (rerr error) {
			defer func() { rerr = Defer(rerr) }()

			// TODO: better context ?
			ctx := context.Background()

			body := resp.Body

			ce := resp.Header.Get("Content-Encoding")
			// Since the proxy is already decompressing the stream, no need to recompress it for the agent and have them decompress it again
			// So remove Content-Encoding, indicating an uncompressed stream.
			resp.Header.Del("Content-Encoding")
			switch ce {
			case "br":
				body = io.NopCloser(brotli.NewReader(body))
			case "gzip":
				var err error
				body, err = gzip.NewReader(body)
				if err != nil {
					return err
				}
			case "":
			default:
				return fmt.Errorf("unhandled Content-Encoding %s", ce)
			}

			ct := resp.Header.Get("Content-Type")
			fmt.Println("TOTO content-type", ct)
			if ct != "" {
				mediaType, params, err := mime.ParseMediaType(ct)
				fmt.Println("QIQI", mediaType, params, err)
				if err != nil {
					return err
				}
				if charset, ok := params["charset"]; ok && charset != "utf-8" {
					return fmt.Errorf("unhandled mime type params %q", params["charset"])
				}
				ct = mediaType
			}

			if ct != "text/event-stream" {
				out := ctxio.NewReaderFanOut(ctx, body, 2)
				var dupBody io.ReadCloser
				resp.Body, dupBody = out.Readers[0], out.Readers[1]

				fmt.Printf("=== RESPONSE [%d] ===\n\n", numRequests)
				go func() {
					defer fmt.Println("\n\nDONE RESPONSE\n")
					defer m.Unlock()
					io.Copy(os.Stdout, dupBody)
				}()
				return nil
			}
			fmt.Println("SSE")
			sseReader := sse.NewEventStreamReader(body)
			var pw *io.PipeWriter
			resp.Body, pw = io.Pipe()

			// TODO: context?
			go func() {
				defer m.Unlock()
				for {
					msg, err := sseReader.ReadEvent()
					if err != nil {
						pw.CloseWithError(err)
						return
					}
					encodingBase64 := false
					event, err := processEvent(msg, encodingBase64)
					if err != nil {
						panic(err)
					}
					fmt.Printf("FOUND EVENT: %s: %s\n", event.Event, event.Data)
					_, err = pw.Write(msg)
					if err != nil {
						panic(err)
					}
				}
			}()

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
	agentID := R(ctx, "docker run --init -dit --rm --net container:%s -v /tmp/claude.json:/root/.claude.json -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 -e ANTHROPIC_BASE_URL=http://localhost:8080 -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -v /tmp/claude.json:/root/.claude.json -w /root/vibing cosmos-agent:claude", M2(os.Hostname()))
	fmt.Println(agentID)
	enc := json.NewEncoder(conn)
	M(enc.Encode(agentID))
	fmt.Println("waiting")
	R(ctx, "docker wait %s", agentID)
}
