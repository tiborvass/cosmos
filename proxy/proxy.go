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
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/andybalholm/brotli"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/r3labs/sse"
	"github.com/tiborvass/cosmos/ctxio"
	. "github.com/tiborvass/cosmos/utils"
)

const (
	ANTHROPIC_BASE_DOMAIN = "api.anthropic.com"
)

var (
	numRequests = 0
	toolUseIDs  = map[string]struct{}{}
	logger      *log.Logger
)

func init() {
	// Log to a file instead of stdout to avoid conflicts with Claude's TUI
	logFile, err := os.OpenFile("/tmp/cosmos-proxy.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Fallback to stderr if we can't open the log file
		logger = log.New(os.Stderr, "[PROXY] ", log.LstdFlags)
	} else {
		logger = log.New(logFile, "[PROXY] ", log.LstdFlags)
	}
}

func startProxy(addr string) *http.Server {
	logger.Printf("Proxy listening on %s\n", addr)

	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()

			ctx := pr.In.Context()
			rout := ctxio.NewReaderFanOut(ctx, pr.Out.Body, 2)
			var dupBody io.ReadCloser
			pr.Out.Body, dupBody = rout.Readers[0], rout.Readers[1]

			numRequests++
			logger.Printf("=== [REQUEST %d] ===\n\n", numRequests)

			go func() {
				defer rout.Close()
				defer logger.Println("\n\nDONE REQUEST")
				io.Copy(os.Stdout, dupBody)
			}()

			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			pr.Out.Host = ANTHROPIC_BASE_DOMAIN

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
				logger.Printf("=== RESPONSE [%d] ===\n\n", numRequests)
				go func() {
					defer logger.Println("\n\nDONE RESPONSE")
					defer m.Unlock()
					io.Copy(os.Stdout, dupBody)
				}()
				return nil
			}
			logger.Println("\n\nSSE")
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
								logger.Printf("FOUND EVENT: %s: %v\n", event.Event, content.ID)
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

func main() {
	logger.Println("Starting proxy...")
	defer logger.Println("Proxy shutdown complete")
	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer func() {
		if x := recover(); x != nil {
			logger.Printf("Panic: %v", x)
		}
		stop()
	}()

	proxy := startProxy(":8080")

	// Wait for shutdown signal
	<-ctx.Done()

	logger.Println("Shutting down proxy...")
	if err := proxy.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}
