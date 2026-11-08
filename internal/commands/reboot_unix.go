//go:build !windows

package commands

import (
	"context"
	"os/exec"
	"strconv"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

// shutdown(8) takes whole minutes, so the delay rounds up.
func scheduleReboot(ctx context.Context, delay time.Duration, msg string, out *Output) error {
	exe := "/sbin/shutdown"
	if p, err := exec.LookPath("shutdown"); err == nil {
		exe = p
	}
	mins := int((delay + time.Minute - 1) / time.Minute)
	args := []string{"-r", "+" + strconv.Itoa(mins)}
	if msg != "" {
		args = append(args, msg)
	}
	if err := execx.Run(ctx, time.Minute, out, nil, exe, args...); err != nil {
		return err
	}
	out.Printf("Reboot scheduled in %d minute(s).", mins)
	return nil
}
