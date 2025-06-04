package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	. "github.com/tiborvass/cosmos/utils"
)

var logFile *os.File

func init() {
	logFile, _ = os.Create("/tmp/cosmos.log")
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos <coding-agent> [<coding-agent-option>...]")
	fmt.Fprintln(os.Stderr, "coding-agent: only \"claude\" is currently supported")
}

func manage(ctx context.Context, clientID string, conn net.Conn) {
	d := json.NewDecoder(conn)
	var x struct {
		Action string
	}
	for {
		if err := d.Decode(&x); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			panic(err)
		}
		switch x.Action {
		case "commit":
			snapshotID := R(ctx, "docker commit %s", clientID)
			fmt.Fprintln(logFile, "Snapshotted", snapshotID)
		}
	}
}

func main() {
	if len(os.Args) <= 1 {
		usage()
		os.Exit(1)
	}
	codingAgent := os.Args[1]
	if codingAgent != "claude" {
		usage()
		os.Exit(1)
	}

	img := "cosmos"
	if len(os.Args) > 2 {
		img = os.Args[2]
	}

	ctx := context.Background()

	// Read claude configuration
	claudeJSONBytes := M2(os.ReadFile("/tmp/claude.json"))
	var claudeJSON map[string]any
	M(json.Unmarshal(claudeJSONBytes, &claudeJSON))
	projects, ok := claudeJSON["projects"].(map[string]any)
	if !ok {
		panic(fmt.Errorf("\"projects\" key in .claude.json is expected to be an object but is %T: %+v\n", claudeJSON["projects"], claudeJSON["projects"]))
	}
	// Mask other projects
	claudeJSON["projects"] = map[string]any{
		"/w": projects["/root/vibing"],
	}
	claudeJSONBytes = M2(json.Marshal(claudeJSON))

	workdir := M2(os.Getwd())

	// Build docker run command for the combined container
	// dockerArgs := fmt.Sprintf("docker run --init --rm -v %s:%s -v /tmp/claude.json:/root/.claude.json -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -w %s -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 cosmos", workdir, workdir, workdir)
	dockerArgs := fmt.Sprintf("docker run -d --init -P --rm -h cosmos --tmpfs /cosmos -v %s:/root/vibing -v /tmp/claude.json:/root/.claude.json -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -w /root/vibing -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 %s", workdir, img)

	// Add -it if we have a TTY
	if isatty.IsTerminal(os.Stdin.Fd()) {
		dockerArgs = strings.Replace(dockerArgs, "docker run ", "docker run -it ", 1)
	}

	args := strings.Fields(dockerArgs)
	args = append(args, os.Args[2:]...)

	fmt.Fprintln(logFile, args)

	// Run the container directly with stdin/stdout/stderr attached
	clientID := RS(ctx, args)

	// Create a channel to receive OS signals.
	sigs := make(chan os.Signal, 1)
	// Notify the channel on SIGINT (Ctrl+C) or SIGTERM
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		fmt.Fprintln(logFile, "received signal", sig)
		exec.Command("docker", "stop", clientID).Run()
	}()

	clientPort := "8042"
	clientAddr := R(ctx, "docker port %s %s/tcp", clientID, clientPort)

	fmt.Fprintln(logFile, "connecting to client", clientAddr)
	dialer := &net.Dialer{}
	var (
		err  error
		conn net.Conn
	)

	maxRetries := 5
	backoff := time.Second / 2
	for range maxRetries {
		conn, err = dialer.DialContext(ctx, "tcp", clientAddr)
		if err == nil {
			break
		}
		fmt.Fprintf(logFile, "unable to connect to cosmos-manager (%s %s): %v, retrying in %v...\n", clientID, clientAddr, err, backoff)
		time.Sleep(backoff)
		backoff *= 2
	}
	fmt.Fprintln(logFile, "connected to manager")
	if err != nil {
		panic(fmt.Errorf("failed to connect after %d retries: %v", maxRetries, err))
	}

	go manage(ctx, clientID, conn)

	cmd := exec.CommandContext(ctx, "docker", "attach", clientID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		panic(err)
	}
}
