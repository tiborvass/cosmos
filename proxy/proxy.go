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
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/mattn/go-isatty"
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
	logFile, err := os.OpenFile("/cosmos/proxy.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Fallback to stderr if we can't open the log file
		logger = log.New(os.Stderr, "\n[PROXY] ", log.LstdFlags)
	} else {
		logger = log.New(logFile, "\n[PROXY] ", log.LstdFlags)
	}
}

type Proxy struct {
	http.Server
	manager *json.Encoder
	tt      *ToolsTracker
	// w       *fsnotify.Watcher
}

func (p *Proxy) Close() {
	// p.w.Close()
}

type set struct {
	m sync.Mutex
	s map[string]struct{}
}

func (s *set) Add(key string) {
	s.m.Lock()
	s.s[key] = struct{}{}
	s.m.Unlock()
}

func (s *set) Remove(key string) (n int) {
	s.m.Lock()
	delete(s.s, key)
	n = len(s.s)
	s.m.Unlock()
	return
}

func startProxy(addr string, managerConn net.Conn) *Proxy {
	logger.Printf("Proxy listening on %s\n", addr)

	var (
		m sync.Mutex
	)

	s := &Proxy{
		Server:  http.Server{Addr: addr},
		manager: json.NewEncoder(managerConn),
	}

	// s.w, err = fsnotify.NewWatcher()
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// s.w.Add("/root/.claude/projects/-root-vibing")

	// trackerCh := make(chan *ToolsTracker)

	// // Start listening for fs events.
	// go func() {
	// 	for {
	// 		select {
	// 		case event, ok := <-s.w.Events:
	// 			if !ok {
	// 				return
	// 			}
	// 			if event.Has(fsnotify.Create) {
	// 				name := event.Name
	// 				if !strings.HasSuffix(name, ".jsonl") {
	// 					log.Println("unexpected event", event.Name, event)
	// 					continue
	// 				}
	// 				tt, err := ProcessLogFileForCompletions(event.Name)
	// 				if err != nil {
	// 					log.Printf("error processing log %q: %v", event.Name, err)
	// 				}
	// 				select {
	// 				case trackerCh <- tt:
	// 				}
	// 				return
	// 			}
	// 		case err, ok := <-s.w.Errors:
	// 			if !ok {
	// 				return
	// 			}
	// 			log.Println("error:", err)
	// 		}
	// 	}
	// }()

	// var mTools sync.Mutex
	// var toolsCh chan struct{}
	// waitTools := make(chan struct{}, 1)
	toolsQueue := &set{s: map[string]struct{}{}}
	// toolsDone := &set{s: map[string]struct{}{}}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()

			ctx := pr.In.Context()
			rout := ctxio.NewReaderFanOut(ctx, pr.Out.Body, 2)
			var dupBody io.ReadCloser
			pr.Out.Body, dupBody = rout.Readers[0], rout.Readers[1]

			numRequests++
			ct := pr.Out.Header.Get("Content-Type")
			logger.Printf("=== [REQUEST %d: %s] ===\n\n", numRequests, ct)

			go func() {
				defer rout.Close()
				defer logger.Println("\n\nDONE REQUEST")
				io.Copy(logger.Writer(), dupBody)
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
			resp.Body, dupBody = rout.Readers[0], io.NopCloser(io.TeeReader(rout.Readers[1], logger.Writer()))

			if ct != "text/event-stream" {
				logger.Printf("=== RESPONSE [%d] ===\n\n", numRequests)
				go func() {
					defer logger.Println("\n\nDONE RESPONSE")
					defer m.Unlock()
					io.Copy(logger.Writer(), dupBody)
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

				// logger.Println("\n\nWaiting for tracker")
				// wait for session file to be created
				// var tt *ToolsTracker
				// select {
				// case tt = <-trackerCh:
				// }
				// logger.Println("\n\nTracker found")
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
						logger.Println("\n\n===TOTO===", msg)
						// Just in case Claude does not accumulate like we do, and starts executing tools as it streams partial json
						// there could be a race, where Claude executes a tool, writes to jsonlog before we get to AddPendingTool.
						// FIXME: Tracker should not delete from a map, it should just have 2 maps: one for what's gonna be executed
						// one for what's been executed, and we should compare the two.
						for _, content := range msg.Content {
							if content.Type == "tool_use" {
								// For some reason msg.ToolUseID is empty
								toolUseID := content.ID
								toolsQueue.Add(toolUseID)
								// tt.AddPendingTool(toolUseID)
								logger.Printf("FOUND EVENT: %s: %v\n", event.Event, toolUseID)
							}
						}
						// Prompt is released to user.
						// TODO: what to do if user add prompts to the queue of prompts ?
						if msg.StopReason == anthropic.StopReasonEndTurn {
							logger.Println("acquiring commit lock")
							toolsQueue.m.Lock()
							if len(toolsQueue.s) > 0 {
								s.commit()
							}
							logger.Println("committing ", toolsQueue.s)
							toolsQueue.s = map[string]struct{}{}
							toolsQueue.m.Unlock()
							logger.Println("releasing commit lock")
						}
						*msg = anthropic.Message{}
					}
				}
			}()

			return nil
		},
	}

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

	// Wait for proxy to be ready (silently)
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			break
		}
		if i == maxRetries-1 {
			panic(fmt.Errorf("proxy failed to start after %d attempts", maxRetries))
		}
		time.Sleep(500 * time.Millisecond)
	}

	return s
}

func (p *Proxy) commit() {
	var x = struct {
		Action string
	}{
		"commit",
	}
	logger.Println("Sending commit instruction")
	M(p.manager.Encode(x))
}

// Client should not be Client but the subject of the manager
func startManagerClient(addr string) net.Conn {
	l := M2(net.Listen("tcp", addr))
	logger.Println("listening on ", addr)
	conn := M2(l.Accept())
	return conn
}

func main() {
	logger.Println("Starting proxy...", isatty.IsTerminal(os.Stdin.Fd()))
	defer logger.Println("Proxy shutdown complete")
	ctx := context.Background()
	// ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	// defer func() {
	// 	if x := recover(); x != nil {
	// 		logger.Printf("Panic: %v", x)
	// 	}
	// 	stop()
	// }()

	managerAddr := "0.0.0.0:8042"
	logger.Println("START", managerAddr)
	managerConn := startManagerClient(managerAddr)

	logger.Println("Client started")

	proxyAddr := "localhost:8080"
	proxy := startProxy(proxyAddr, managerConn)

	logger.Println("Proxy started")

	// Execute claude with all arguments passed to the entrypoint
	claudeCmd := exec.CommandContext(ctx, "/usr/local/bin/claude", os.Args[1:]...)
	claudeCmd.Env = append(os.Environ(), "ANTHROPIC_BASE_URL=http://"+proxyAddr)
	claudeCmd.Stdin = os.Stdin
	claudeCmd.Stdout = os.Stdout
	claudeCmd.Stderr = os.Stderr

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigch
		claudeCmd.Process.Signal(sig)
	}()

	// Run claude and wait for it to complete
	err := claudeCmd.Run()

	// Exit with claude's exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		panic(err)
	}

	logger.Println("Shutting down proxy...")
	if err := proxy.Shutdown(context.Background()); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}
