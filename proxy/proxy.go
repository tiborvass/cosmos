package main

import (
	"bytes"
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
	"golang.org/x/sys/unix"
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
	cancel  func()
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

type tr struct{}

func (tr) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		logger.Println("roundtrip error:", err)
	}
	return resp, err
}

func startProxy(addr string, managerConn net.Conn, cancel context.CancelFunc) *Proxy {
	logger.Printf("Proxy listening on %s\n", addr)

	var (
		m sync.Mutex
	)

	s := &Proxy{
		Server:  http.Server{Addr: addr},
		manager: json.NewEncoder(managerConn),
		cancel:  cancel,
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

	var allReqsData [][]byte

	proxy := &httputil.ReverseProxy{
		Transport: tr{},
		Rewrite: func(pr *httputil.ProxyRequest) {
			m.Lock()

			ctx := pr.In.Context()
			numRequests++
			ct := pr.Out.Header.Get("Content-Type")
			logger.Printf("=== [REQUEST %d: %s] ===\n\n", numRequests, ct)

			rout := ctxio.NewReaderFanOut(ctx, pr.Out.Body, 2)
			var dupBody io.ReadCloser
			pr.Out.Body, dupBody = rout.Readers[0], rout.Readers[1]

			go func() {
				defer rout.Close()
				defer logger.Println("\n\nDONE REQUEST")
				if ct != "application/json" {
					io.Copy(logger.Writer(), dupBody)
					return
				}
				var x struct {
					Messages []json.RawMessage
				}
				NoEOF(json.NewDecoder(io.TeeReader(dupBody, logger.Writer())).Decode(&x))

				switch len(x.Messages) {
				case 0:
					err := fmt.Errorf("unexpected number of messages: %d", len(x.Messages))
					logger.Printf("===ERROR===: %v\n", err)
					return
				case 1:
					return
				}
				// Get the N-2 message: the user message that contains the last tool_result
				var msg struct {
					Role    string
					Content json.RawMessage
				}
				M(json.Unmarshal([]byte(x.Messages[len(x.Messages)-3]), &msg))
				if msg.Role != "user" {
					logger.Printf("===ERROR===: expected role assistant got %q\n", msg.Role)
					return
				}
				logger.Printf("===MSG===: %#v\n", msg)

				// Only account for msg.Content, because other fields in msg can vary (notably "cache_control" field)
				reqData := M2(json.Marshal(msg.Content))
				// Remove '[' and ']' so we can manually JSON decode in a loop the common prefix which may not be at a valid JSON boundary.
				reqData = reqData[1 : len(reqData)-1]
				// reqData := M2(io.ReadAll(io.TeeReader(dupBody, logger.Writer())))
				// M(json.Unmarshal(reqData, &x))
				// Sometimes model is different, so match only starting from messages
				// i := bytes.Index(reqData, []byte(`"messages":[`))
				// reqData = reqData[i:]
				// allReqsData = append(allReqsData, reqData)

				if msg.Content[0] == '"' {
					logger.Println("===STRCONTENT===", string(msg.Content))
					return
				}
				var contents []struct {
					ToolUseID string
					Type      string
					Content   json.RawMessage
				}
				M(json.Unmarshal(msg.Content, &contents))
				logger.Println("===REQDATA===", string(reqData))
				for i := len(contents) - 1; i >= 0; i-- {
					content := contents[i]
					logger.Printf("===CONTENT===: %+v\n", content)
					if content.Type == "tool_result" {
						logger.Println("===TOOL_RESULT===", content.ToolUseID)
						// maxPrefixJ, maxPrefixLen := -1, 0
						for j := len(allReqsData) - 1; j >= 0; j-- {
							prevReqData := allReqsData[j]
							logger.Println("===PREVREQDATA===", j, string(prevReqData))
							prefix := CommonPrefixBytes(reqData, prevReqData)
							// // Skip "[" to have a list of comma-separated JSON objects
							r := bytes.NewReader(prefix)
							// Maybe there's a faster way to extract the valid JSON objects from the []byte assuming the JSON has a list of valid objects
							var v json.RawMessage
							var n int
							for {
								d := json.NewDecoder(r)
								if err := d.Decode(&v); err != nil {
									break
								}
								// decode is successful, keep track how many bytes take up the valid JSONs so far
								leftInBuf, _ := io.Copy(io.Discard, d.Buffered())
								// total unread = unread bytes reader portion + what's still in JSON buffer
								n = r.Len() + int(leftInBuf)
								// skip ","
								r.Seek(1, io.SeekCurrent)
							}

							prefix = prefix[:len(prefix)-n]
							logger.Println("===PREFIX===", j, string(prefix))
							if len(prefix) == len(prevReqData) {
								logger.Println("===!!!!!===", j)
								//&& len(prefix) > maxPrefixLen {
								//maxPrefixLen = len(prefix)
								// maxPrefixJ = j
								s.load(j - 1)
							}
						}
						allReqsData = append(allReqsData, reqData)
						// if maxPrefixJ >= 0 {
						// logger.Println("===!!!!!===", maxPrefixJ)
						// }
						break
					}
				}
			}()

			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = ANTHROPIC_BASE_DOMAIN
			pr.Out.Host = ANTHROPIC_BASE_DOMAIN
		},
		ModifyResponse: func(resp *http.Response) (rerr error) {
			defer func() {
				rerr = Defer(rerr)
				if rerr != nil {
					rerr = fmt.Errorf("toto: %w", rerr)
				}
			}()
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
						toolUseID := ""
						// Just in case Claude does not accumulate like we do, and starts executing tools as it streams partial json
						// there could be a race, where Claude executes a tool, writes to jsonlog before we get to AddPendingTool.
						// FIXME: Tracker should not delete from a map, it should just have 2 maps: one for what's gonna be executed
						// one for what's been executed, and we should compare the two.
						for _, content := range msg.Content {
							if content.Type == "tool_use" {
								// For some reason msg.ToolUseID is empty
								toolUseID = content.ID
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
								// TODO: find summary of what was done, or make the commits per tool use
								s.commit(toolUseID)
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
	for i := range maxRetries {
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

func (p *Proxy) load(historyIndex int) {
	var x = struct {
		Action string
		Data   int
	}{
		"load",
		historyIndex,
	}
	logger.Println("Sending load instruction")
	if err := p.manager.Encode(x); err == io.EOF {
		p.cancel()
	} else if err != nil {
		panic(err)
	}
}

func (p *Proxy) commit(comment string) {
	var x = struct {
		Action string
		Data   string
	}{
		"commit",
		comment,
	}
	logger.Println("Sending commit instruction")
	if err := p.manager.Encode(x); err == io.EOF {
		p.cancel()
	} else if err != nil {
		panic(err)
	}
}

// Client should not be Client but the subject of the manager
func startManagerClient(addr string) net.Conn {
	l := M2(net.Listen("tcp", addr))
	defer l.Close()
	logger.Println("listening on ", addr)
	conn := M2(l.Accept())
	logger.Println("accepted conn", conn.RemoteAddr())
	return conn
}

func main() {
	logger.Println("Starting proxy...", isatty.IsTerminal(os.Stdin.Fd()))
	defer logger.Println("Proxy shutdown complete")
	ctx, cancel := context.WithCancel(context.Background())

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
	proxy := startProxy(proxyAddr, managerConn, cancel)

	logger.Println("Proxy started")

	// Execute claude with all arguments passed to the entrypoint
	claudeCmd := exec.CommandContext(ctx, "/usr/local/bin/claude", os.Args[1:]...)
	claudeCmd.Env = append(os.Environ(), "ANTHROPIC_BASE_URL=http://"+proxyAddr)
	claudeCmd.Stdin = os.Stdin
	claudeCmd.Stdout = os.Stdout
	claudeCmd.Stderr = os.Stderr

	logger.Println(claudeCmd.Env)

	// Create a channel to receive OS signals.
	sigch := make(chan os.Signal, 1)
	// Notify the channel on SIGINT (Ctrl+C) or SIGTERM
	signal.Notify(sigch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGQUIT, syscall.SIGHUP)

	go func() {
		for {
			sig := <-sigch
			if sig, ok := sig.(syscall.Signal); ok {
				name := unix.SignalName(sig)
				logger.Println("received signal", name, int(sig), ":", sig.String())
			} else {
				logger.Println("received signal", sig)
			}
			claudeCmd.Process.Signal(sig)
		}
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

func CommonPrefixBytes(a, b []byte) []byte {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	i := 0
	for i < minLen && a[i] == b[i] {
		i++
	}
	return a[:i]
}
