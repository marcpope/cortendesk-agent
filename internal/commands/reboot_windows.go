package commands

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

func scheduleReboot(ctx context.Context, delay time.Duration, msg string, out *Output) error {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "shutdown.exe")
	args := []string{"/r", "/t", strconv.Itoa(int(delay / time.Second)), "/d", "p:0:0"}
	if msg != "" {
		args = append(args, "/c", msg)
	}
	if err := execx.Run(ctx, time.Minute, out, nil, exe, args...); err != nil {
		return err
	}
	out.Printf("Reboot scheduled in %s.", delay)
	return nil
}
