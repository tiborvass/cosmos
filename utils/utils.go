package util

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func M2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func M(err error) {
	if err != nil {
		panic(err)
	}
}

func RS(ctx context.Context, args []string) string {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("%s: %w", string(out), err))
	}
	if out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return string(out)
}

func R(ctx context.Context, format string, args ...any) string {
	return RS(ctx, strings.Fields(fmt.Sprintf(format, args...)))
}
