package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	. "github.com/tiborvass/cosmos/utils"
	"golang.org/x/sys/unix"
)

var logFile *os.File

func init() {
	logFile, _ = os.Create("/tmp/cosmos.log")
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos <coding-agent> [<coding-agent-option>...]")
	fmt.Fprintln(os.Stderr, "coding-agent: only \"claude\" is currently supported")
}

type Snapshot struct {
	ID        string
	Message   string
	SessionID string
}

var state struct {
	Projects map[string]struct {
		Snapshots []Snapshot
	}
}

var imgs = []string{"cosmos"}

func manage(ctx context.Context, clientID string, conn net.Conn) {
	defer func() {
		fmt.Fprintln(logFile, "Closing conn")
		conn.Close()
	}()
	d := json.NewDecoder(conn)
	d.UseNumber()
	var x struct {
		Action string
		Data   any
	}
	for {
		if err := d.Decode(&x); err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(logFile, "EOF")
				return
			}
			panic(err)
		}
		switch x.Action {
		case "commit":
			bytes := make([]byte, 16)
			M2(rand.Read(bytes))
			snapshotID := hex.EncodeToString(bytes)
			// TODO: check if image exists
			imgID := R(ctx, "docker commit -m %q %s cosmos:%s", x.Data, clientID, snapshotID)
			fmt.Fprintln(logFile, "Snapshot", snapshotID, "image", imgID)
			imgs = append(imgs, imgID)
		case "load":
			n := M2(strconv.Atoi(string(x.Data.(json.Number))))
			imgID := imgs[n]
			env := append(os.Environ(), fmt.Sprintf("IMAGE=%s", imgID))
			syscall.Exec(os.Args[0], os.Args, env)
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

	args := os.Args[2:]

	cosmosDir := filepath.Join(M2(os.UserConfigDir()), ".cosmos")
	os.MkdirAll(cosmosDir, 0755)

	stateFile, err := os.Open("~/.cosmos/state.json")
	if err == nil {
		M(json.NewDecoder(stateFile).Decode(&state))
	} else if !os.IsNotExist(err) {
		panic(err)
	}

	img := imgs[0]
	resume := ""
	if v := os.Getenv("IMAGE"); v != "" {
		img = v
		resume = "-r"
	}
	wd := M2(os.Getwd())
	if project, ok := state.Projects[wd]; ok && len(project.Snapshots) > 0 {
		img = project.Snapshots[len(project.Snapshots)-1].ID
	}

	ctx := context.Background()

	// Read claude configuration
	// claudeJSONBytes := M2(os.ReadFile("/tmp/claude.json"))
	// var claudeJSON map[string]any
	// M(json.Unmarshal(claudeJSONBytes, &claudeJSON))
	// projects, ok := claudeJSON["projects"].(map[string]any)
	// if !ok {
	// 	panic(fmt.Errorf("\"projects\" key in .claude.json is expected to be an object but is %T: %+v\n", claudeJSON["projects"], claudeJSON["projects"]))
	// }
	// // Mask other projects
	// claudeJSON["projects"] = map[string]any{
	// 	"/w": projects["/home/cosmos/vibing"],
	// }
	// claudeJSONBytes = M2(json.Marshal(claudeJSON))

	workdir := M2(os.Getwd())

	// Build docker run command for the combined container
	// dockerArgs := fmt.Sprintf("docker run --init --rm -v %s:%s -v /tmp/claude.json:/root/.claude.json -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -w %s -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 cosmos", workdir, workdir, workdir)
	dockerArgs := fmt.Sprintf("docker run -d --init -P --rm -h cosmos --tmpfs /cosmos -v %s:/%s -w %s -v /tmp/claude.json:/home/cosmos/.claude.json -v /tmp/claude.state/.credentials.json:/home/cosmos/.claude/.credentials.json -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 %s %s", workdir, workdir, workdir, img, resume)
	// dockerArgs := fmt.Sprintf("docker run -d --init -P --rm -h cosmos --tmpfs /cosmos -v %s:/%s -w %s -v /tmp/claude.state/.credentials.json:/home/cosmos/.claude/.credentials.json -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 %s", workdir, workdir, workdir, img)

	// Add -it if we have a TTY
	if isatty.IsTerminal(os.Stdin.Fd()) {
		dockerArgs = strings.Replace(dockerArgs, "docker run ", "docker run -it ", 1)
	}

	args = append(strings.Fields(dockerArgs), args...)

	fmt.Fprintln(logFile, args)

	// Run the container directly with stdin/stdout/stderr attached
	clientID := RS(ctx, args)

	// Create a channel to receive OS signals.
	sigs := make(chan os.Signal, 1)
	// Notify the channel on SIGINT (Ctrl+C) or SIGTERM
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGINFO)

	go func() {
		for {
			sig := <-sigs
			if sig, ok := sig.(syscall.Signal); ok {
				name := unix.SignalName(sig)
				fmt.Fprintln(logFile, "received signal", name, int(sig), ":", sig.String())
				exec.Command("docker", "kill", "-s", name, clientID).Run()
			} else {
				fmt.Fprintln(logFile, "received signal", sig)
			}
		}
	}()

	clientPort := "8042"
	clientAddr := R(ctx, "docker port %s %s/tcp", clientID, clientPort)

	fmt.Fprintln(logFile, "connecting to client", clientAddr)
	dialer := &net.Dialer{}
	var (
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
	fmt.Fprintf(logFile, "connected to client %v running in %s\n", conn.RemoteAddr(), clientID)
	if err != nil {
		panic(fmt.Errorf("failed to connect after %d retries: %v", maxRetries, err))
	}

	go manage(ctx, clientID, conn)

	cmd := exec.CommandContext(ctx, "docker", "attach", clientID)
	cmd.Cancel = func() error {
		fmt.Fprintln(logFile, "cancelling", clientID)
		return nil
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		panic(err)
	}
}
