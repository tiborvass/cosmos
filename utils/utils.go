package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
		panic(fmt.Errorf("%s: %w", string(out), err))
	}
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return string(out)
}

func NoEOF(err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	panic(err)
}

func Defer(rerr error) error {
	x := recover()
	if x == nil {
		return rerr
	}
	if err, ok := x.(error); ok {
		return err
	}
	panic(x)
}

func R(ctx context.Context, format string, args ...any) string {
	return RS(ctx, []string{"sh", "-c", fmt.Sprintf(format, args...)})
}
