package main

import (
	"compress/gzip"
	"context"
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
	"github.com/r3labs/sse"
	"github.com/tiborvass/cosmos/ctxio"
	. "github.com/tiborvass/cosmos/utils"
)

const (
	ANTHROPIC_BASE_DOMAIN = "api.anthropic.com"
)

var (
	numRequests = 0
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
			// defer m.Unlock()

			ctx := pr.In.Context()
			body := io.NopCloser(pr.Out.Body)
			// One request at a time
			// Unlock is in ModifyResponse
			numRequests++
			logger.Printf("=== [REQUEST %d] %s %s ===\n", numRequests, pr.Out.Method, pr.Out.URL.Path)

			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			pr.Out.Host = ANTHROPIC_BASE_DOMAIN

			rout := ctxio.NewReaderFanOut(ctx, body, 2)
			var dupBody io.ReadCloser
			pr.Out.Body, dupBody = rout.Readers[0], rout.Readers[1]

			go func() {
				defer rout.Close()
				defer logger.Println("=== END REQUEST ===")
				// Log request body to file
				io.Copy(logger.Writer(), dupBody)
			}()

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
			logger.Printf("Response Content-Type: %s", ct)
			if ct != "" {
				mediaType, params, err := mime.ParseMediaType(ct)
				if err != nil {
					return err
				}
				if charset, ok := params["charset"]; ok && charset != "utf-8" {
					return fmt.Errorf("unhandled charset %s", charset)
				}
				switch mediaType {
				case "text/event-stream":
					defer m.Unlock()
					logger.Printf("=== [RESPONSE %d - SSE] Status: %d ===\n", numRequests, resp.StatusCode)
					rout := ctxio.NewReaderFanOut(ctx, body, 2)
					defer rout.Close()
					resp.Body = io.NopCloser(rout.Readers[1])

					// https://github.com/r3labs/sse/blob/5b0a3bfa0ede4ec1677bb22bfe40b59df1aa9de0/http.go#L42
					scanner := sse.NewEventStreamReader(rout.Readers[0])

					go func() {
						defer logger.Println("=== END SSE RESPONSE ===")
						for {
							msg, err := scanner.ReadEvent()
							if err != nil {
								if errors.Is(err, io.EOF) {
									logger.Println("SSE: EOF")
									return
								}
								// Read error
								logger.Printf("Error parsing SSE: %v", err)
								return
							}

							if msg != nil && len(msg) > 0 {
								event, err := processEvent(msg, false)
								if err != nil {
									logger.Printf("Error processing event: %v", err)
									continue
								}
								if event != nil && len(event.Data) > 0 {
									logger.Printf("SSE Event: %s", string(event.Data))
								}
							}
						}
					}()
					return nil
				case "application/json":
					defer m.Unlock()
					logger.Printf("=== [RESPONSE %d - JSON] Status: %d ===\n", numRequests, resp.StatusCode)
				default:
					panic(fmt.Errorf("unhandled media type %s", mediaType))
				}
			}

			rout := ctxio.NewReaderFanOut(ctx, body, 2)
			defer rout.Close()
			var dupBody io.ReadCloser
			resp.Body, dupBody = io.NopCloser(rout.Readers[0]), rout.Readers[1]

			go func() {
				defer logger.Println("=== END RESPONSE ===")
				io.Copy(logger.Writer(), dupBody)
			}()

			return nil
		},
	}

	server := &http.Server{
		Addr:    addr,
		Handler: proxy,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	return server
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