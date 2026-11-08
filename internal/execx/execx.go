// Package execx runs external commands with a hard timeout.
package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Output runs name with args and returns stdout. The command is killed after
// timeout. Stdin is empty so nothing can wait on a prompt.
func Output(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hide(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	// Don't wait on pipes held open by children after the kill.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), ctx.Err()
	}
	return out.Bytes(), leftPipesOpen(cmd, err)
}

// Run runs name with args, sends stdout and stderr to w, and kills it after
// timeout. env entries are added to the agent's environment.
func Run(ctx context.Context, timeout time.Duration, w io.Writer, env []string, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	hide(cmd)
	cmd.Stdout = w
	cmd.Stderr = w
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return leftPipesOpen(cmd, err)
}

// leftPipesOpen forgives a command that exited 0 but started a process
// still holding its output (an installer starting the RustDesk service).
func leftPipesOpen(cmd *exec.Cmd, err error) error {
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		return nil
	}
	return err
}
