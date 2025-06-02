package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"dagger.io/dagger"
)

var dag *dagger.Client

func M[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cosmos <coding-agent> [<coding-agent-option>...]")
	fmt.Fprintln(os.Stderr, "coding-agent: only \"claude\" is currently supported")
}

func main() {
	if _, ok := os.LookupEnv("DAGGER_SESSION_TOKEN"); !ok {
		args := make([]string, len(os.Args)+2)
		var err error
		args[0], err = exec.LookPath("dagger")
		if err != nil {
			panic("TODO: auto download dagger")
		}
		args[1] = "run"
		copy(args[2:], os.Args)
		err = syscall.Exec(args[0], args, os.Environ())
		panic(fmt.Errorf("unexpected reexec failure %v: %w", args, err))
	}

	if len(os.Args) <= 1 {
		usage()
		return
	}
	codingAgent := os.Args[1]
	if codingAgent != "claude" {
		usage()
		return
	}
	// remainder args
	args := os.Args[2:]
	claudeCmd := []string{"/usr/local/bin/claude"}
	claudeCmd = append(claudeCmd, args...)

	ctx := context.Background()

	var err error
	dag, err = dagger.Connect(context.Background() /* dagger.WithStdio(os.Stdin, os.Stdout, os.Stderr), */, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting dagger: %v\n", err)
		os.Exit(1)
	}
	defer dag.Close()

	// TODO: why do we need withExposedPort ? Can't we just rely on the base image's EXPOSE ?
	svc := dag.Container().From("tiborvass/coding-proxy").WithExposedPort(8080).AsService(dagger.ContainerAsServiceOpts{ExperimentalPrivilegedNesting: false})
	svc = M(dag.Host().Tunnel(svc).Start(ctx))
	// svc.Endpoint(ctx, dagger.ServiceEndpointOpts{})
	ports := M(svc.Ports(ctx))
	if len(ports) != 1 {
		panic("expected to find exposed ports for coding-proxy")
	}
	port := M(ports[0].Port(ctx))

	// creds := dag.Host().SetSecretFile("claude-credentials", "/tmp/claude-credentials.json")
	// creds := dag.Secret("file:///tmp/claude-credentials.json")
	claudeFile := dag.Host().File("/tmp/claude.json")
	claudeState := dag.Host().Directory("/tmp/claude.state")

	ctr := dag.Container().From("node:24.1.0-slim@sha256:5ae787590295f944e7dc200bf54861bac09bf21b5fdb4c9b97aee7781b6d95a2").
		WithMountedCache("$HOME/.npm", dag.CacheVolume("npm"), dagger.ContainerWithMountedCacheOpts{Expand: true}).
		WithExec(strings.Fields("npm install -g @anthropic-ai/claude-code")).
		WithServiceBinding("coding-proxy", svc).
		// TODO: git?
		WithMountedDirectory("/w", dag.Host().Directory(".")).
		WithWorkdir("/w").
		WithEnvVariable("ANTHROPIC_BASE_URL", fmt.Sprintf("http://coding-proxy:%d", port)).
		// TODO: store claude-credentials.json in tmpfs
		// FIXME: Expand doesn't expand $HOME neither in secret uri, nor target path
		// WithMountedSecret("/root/.claude/.credentials.json", creds, dagger.ContainerWithMountedSecretOpts{Expand: true}).
		WithMountedFile("$HOME/.claude.json", claudeFile, dagger.ContainerWithMountedFileOpts{Expand: true}).
		WithMountedDirectory("$HOME/.claude", claudeState, dagger.ContainerWithMountedDirectoryOpts{Expand: true}).
		// Terminal(dagger.ContainerTerminalOpts{Cmd: []string{"/bin/bash"}})
		Terminal(dagger.ContainerTerminalOpts{Cmd: claudeCmd})

	_, err = ctr.Sync(ctx)
	if err != nil {
		panic(err)
	}

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
