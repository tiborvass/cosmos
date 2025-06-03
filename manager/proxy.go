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
	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/r3labs/sse"
	"github.com/tiborvass/cosmos/ctxio"
	. "github.com/tiborvass/cosmos/utils"
)

const (
	ANTHROPIC_BASE_DOMAIN = "api.anthropic.com"
)

var numRequests = 0

var toolUseIDs = map[string]struct{}{}

func startProxy(addr string) *http.Server {
	log.Printf("Proxy listening on %s\n", addr)

	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()
			// defer m.Unlock()

			// ctx := pr.In.Context()
			pr.Out.Body = io.NopCloser(io.TeeReader(pr.Out.Body, os.Stdout))
			// One request at a time
			// Unlock is in ModifyResponse
			numRequests++
			log.Printf("=== [REQUEST %d] ===\n\n", numRequests)

			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			pr.Out.Host = ANTHROPIC_BASE_DOMAIN

			// rout := ctxio.NewReaderFanOut(ctx, body, 2)
			// var dupBody io.ReadCloser
			// pr.Out.Body, dupBody = rout.Readers[0], rout.Readers[1]

			// go func() {
			// 	defer rout.Close()
			// 	defer fmt.Println("\n\nDONE REQUEST\n\n")
			// 	io.Copy(os.Stdout, dupBody)
			// }()

		},
		ModifyResponse: func(resp *http.Response) (rerr error) {
			defer func() { rerr = Defer(rerr) }()
			// m.Lock()
			// defer m.Unlock()

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
			if ct != "" {
				mediaType, params, err := mime.ParseMediaType(ct)
				if err != nil {
					return err
				}
				if charset, ok := params["charset"]; ok && charset != "utf-8" {
					return fmt.Errorf("unhandled mime type params %q", params["charset"])
				}
				ct = mediaType
			}

			rout := ctxio.NewReaderFanOut(ctx, io.NopCloser(body), 2)
			var dupBody io.ReadCloser
			resp.Body, dupBody = rout.Readers[0], rout.Readers[1]

			if ct != "text/event-stream" {
				fmt.Printf("=== RESPONSE [%d] ===\n\n", numRequests)
				go func() {
					defer fmt.Println("\n\nDONE RESPONSE\n")
					defer m.Unlock()
					io.Copy(os.Stdout, dupBody)
				}()
				return nil
			}
			fmt.Println("\n\nSSE\n")
			sseReader := sse.NewEventStreamReader(dupBody)

			// TODO: context?
			go func() {
				defer rout.Close()
				defer m.Unlock()
				encodingBase64 := false
				msg := new(anthropic.Message)
				for {
					p, err := sseReader.ReadEvent()
					if err != nil {
						return
					}
					event := M2(processEvent(p, encodingBase64))
					var ev anthropic.MessageStreamEventUnion
					M(json.Unmarshal(event.Data, &ev))
					M(msg.Accumulate(ev))
					if _, ok := ev.AsAny().(anthropic.MessageStopEvent); ok {
						for _, content := range msg.Content {
							if content.Type == "tool_use" {
								// For some reason msg.ToolUseID is empty
								toolUseID := content.ID
								// TODO: locks
								toolUseIDs[toolUseID] = struct{}{}
								fmt.Printf("FOUND EVENT: %s: %v\n", event.Event, content.ID)
							}
						}
						*msg = anthropic.Message{}
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
	agentID := R(ctx, "docker run --init -dit --rm --net container:%s -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 -e ANTHROPIC_BASE_URL=http://localhost:8080 -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -v /tmp/claude.json:/root/.claude.json -w /root/vibing cosmos-agent:claude", M2(os.Hostname()))
	fmt.Println(agentID)
	enc := json.NewEncoder(conn)
	M(enc.Encode(agentID))
	fmt.Println("waiting")
	R(ctx, "docker wait %s", agentID)
}
