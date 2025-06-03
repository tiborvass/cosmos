package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/tiborvass/cosmos/coding-proxy/httputil"
)

var numRequests int


type ContainerCommand struct {
	Action      string   `json:"action"`
	Image       string   `json:"image,omitempty"`
	Args        []string `json:"args,omitempty"`
	ContainerID string   `json:"container_id,omitempty"`
}

func handleContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cmd ContainerCommand
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	switch cmd.Action {
	case "start":
		containerID, err := startContainer(cmd.Image, cmd.Args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(containerID))

	case "stop":
		if err := stopContainer(cmd.ContainerID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("OK"))

	case "restart":
		if err := restartContainer(cmd.ContainerID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("OK"))

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func startContainer(imageName string, args []string) (string, error) {
	cmdArgs := []string{"run", "-d", "--network", "cosmos-net", imageName}
	if len(args) > 0 {
		cmdArgs = append(cmdArgs, args...)
	} else {
		// Default command to keep container running
		cmdArgs = append(cmdArgs, "tail", "-f", "/dev/null")
	}

	cmd := exec.Command("docker", cmdArgs...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

func stopContainer(containerID string) error {
	cmd := exec.Command("docker", "stop", containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

func restartContainer(containerID string) error {
	cmd := exec.Command("docker", "restart", containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart container: %w", err)
	}
	return nil
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/container", handleContainer)
	
	var m sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Handle container management requests
			if pr.In.URL.Path == "/container" {
				return
			}
			// Default proxy behavior for other requests
			m.Lock()
			defer m.Unlock()
			fmt.Printf("inp: %+v\n", pr.In)
			fmt.Println()
			fmt.Printf("out: %+v\n", pr.Out)
			fmt.Println()
			fmt.Println()
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request.URL.Path == "/container" {
				return nil
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
				return err
			}

			log.Printf("===[RESPONSE %d]===\n\n%s\n\n", numRequests, respDump)
			return nil
		},
	}
	
	mux.Handle("/", proxy)
	
	log.Printf("Container management and proxy server listening on :8080\n")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		panic(err)
	}
}
