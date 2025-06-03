package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-isatty"
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
	dockerArgs := fmt.Sprintf("docker run --init --rm -v %s:%s -v /tmp/claude.json:/root/.claude.json -v /tmp/claude.state/.credentials.json:/root/.claude/.credentials.json -w %s -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 cosmos", workdir, workdir, workdir)

	// Add -it if we have a TTY
	if isatty.IsTerminal(os.Stdin.Fd()) {
		dockerArgs = strings.Replace(dockerArgs, "docker run", "docker run -it", 1)
	}

	args := strings.Fields(dockerArgs)
	args = append(args, os.Args[2:]...)

	// Run the container directly with stdin/stdout/stderr attached
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		panic(err)
	}
}
