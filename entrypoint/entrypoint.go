package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	. "github.com/tiborvass/cosmos/utils"
)

func main() {
	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the proxy in a goroutine (no arguments needed)
	proxyCmd := exec.CommandContext(ctx, "/usr/local/bin/proxy")
	// Redirect proxy output to /dev/null to keep claude's TUI clean
	proxyCmd.Stdout = nil
	proxyCmd.Stderr = nil
	M(proxyCmd.Start())

	// Wait for proxy to be ready (silently)
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		conn, err := net.DialTimeout("tcp", "localhost:8080", time.Second)
		if err == nil {
			conn.Close()
			break
		}
		if i == maxRetries-1 {
			panic(fmt.Errorf("proxy failed to start after %d attempts", maxRetries))
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Set environment variable for claude
	os.Setenv("ANTHROPIC_BASE_URL", "http://localhost:8080")

	// Execute claude with all arguments passed to the entrypoint
	claudeCmd := exec.CommandContext(ctx, "/usr/local/bin/claude", os.Args[1:]...)
	claudeCmd.Stdin = os.Stdin
	claudeCmd.Stdout = os.Stdout
	claudeCmd.Stderr = os.Stderr

	// Run claude and wait for it to complete
	err := claudeCmd.Run()

	// Stop the proxy
	proxyCmd.Process.Signal(syscall.SIGTERM)
	proxyCmd.Wait()

	// Exit with claude's exit code
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		panic(err)
	}
}