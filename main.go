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
	"strings"
	"time"

	. "github.com/tiborvass/cosmos/utils"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos <coding-agent> [<coding-agent-option>...]")
	fmt.Fprintln(os.Stderr, "coding-agent: only \"claude\" is currently supported")
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

	ctx := context.Background()

	// claudeJSONBytes := []byte(M2(dag.Host().File("/tmp/claude.json").Contents(ctx)))
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
	// claudeFile := dag.File(".claude.json", string(claudeJSONBytes))

	// claudeCreds := M2(os.ReadFile("/tmp/claude-credentials.json"))

	workdir := M2(os.Getwd())
	args := strings.Fields(fmt.Sprintf("docker run -d -p 8042 -v %s:/src -v /tmp/claude.json:/tmp/claude.json -v /tmp/claude-credentials.json:/tmp/claude-credentials.json -v /var/run/docker.sock:/var/run/docker.sock cosmos-manager claude", workdir))
	args = append(args, os.Args[2:]...)

	managerID := RS(ctx, args)
	defer exec.Command("docker", "stop", managerID).Run()

	addr := R(ctx, "docker port %s 8042/tcp", managerID)

	dialer := &net.Dialer{}
	var (
		err  error
		conn net.Conn
	)
	fmt.Println("connecting to manager")
	maxRetries := 5
	backoff := time.Second / 2
	for range maxRetries {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "unable to connect to cosmos-manager (%s %s): %v, retrying in %v...\n", managerID, addr, err, backoff)
		time.Sleep(backoff)
		backoff *= 2
	}
	fmt.Println("connected to manager")
	if err != nil {
		panic(fmt.Errorf("failed to connect after %d retries: %v", maxRetries, err))
	}
	var agentID string
	d := json.NewDecoder(conn)
	err = d.Decode(&agentID)
	if err != nil && !errors.Is(err, io.EOF) {
		panic(err)
	}
	time.Sleep(time.Second * 30)
	fmt.Println("agent", agentID)

	cmd := exec.CommandContext(ctx, "docker", "attach", agentID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println(agentID)
	}

	// ctr := dag.Container().From("node:24.1.0-slim@sha256:5ae787590295f944e7dc200bf54861bac09bf21b5fdb4c9b97aee7781b6d95a2").
	// 	WithMountedCache("$HOME/.npm", dag.CacheVolume("npm"), dagger.ContainerWithMountedCacheOpts{Expand: true}).
	// 	WithExec(strings.Fields("npm install -g @anthropic-ai/claude-code")).
	// 	WithServiceBinding("coding-proxy", svc).
	// 	// TODO: git?
	// 	WithMountedDirectory("/w", dag.Host().Directory(".")).
	// 	WithWorkdir("/w").
	// 	WithEnvVariable("ANTHROPIC_BASE_URL", fmt.Sprintf("http://coding-proxy:%d", port)).
	// 	// TODO: store claude-credentials.json in tmpfs
	// 	// FIXME: Expand doesn't expand $HOME neither in secret uri, nor target path
	// 	// WithMountedSecret("/root/.claude/.credentials.json", creds, dagger.ContainerWithMountedSecretOpts{Expand: true}).
	// 	WithMountedFile("/root/.claude.json", claudeFile).
	// 	// WithMountedDirectory("$HOME/.claude", claudeState, dagger.ContainerWithMountedDirectoryOpts{Expand: true}).
	// 	WithMountedFile("/root/.claude/.credentials.json", claudeCreds).
	// 	// Terminal(dagger.ContainerTerminalOpts{Cmd: []string{"/bin/bash"}})
	// 	Terminal(dagger.ContainerTerminalOpts{Cmd: claudeCmd})

	// M2(ctr.Sync(ctx))

	/*
		cmd := exec.Command("docker", "run", "-it", "--rm", "-v", "/tmp/claude-credentials.json:/root/.claude/.credentials.json", "cosmos:claude")

		cmd.Args = append(cmd.Args, args...)
		// syscall.Exec(cmd.Args[0], cmd.Args, os.Environ())
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			panic(err)
		}
	*/
}
