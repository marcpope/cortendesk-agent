// Package identity finds the values that tie an agent to a RustDesk device:
// the OS machine id (sent by RustDesk as its uuid) and the RustDesk ID.
package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
)

const cmdTimeout = 10 * time.Second

// MachineUUID is base64 (standard encoding) of the OS machine id string, the
// same value RustDesk reports as uuid.
func MachineUUID(ctx context.Context) (string, error) {
	raw, err := machineID(ctx)
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("machine id is empty")
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

func readTrimmed(paths ...string) (string, error) {
	var last error = os.ErrNotExist
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			last = err
			continue
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	return "", last
}

// RustDesk reports whether RustDesk is installed and running, and its
// version. Every failure reads as "not installed" or "unknown".
func RustDesk(ctx context.Context) api.RustDesk {
	var r api.RustDesk
	bin := rustdeskBinary()
	r.Installed = bin != ""
	r.Running = RustDeskRunning()
	if r.Installed {
		r.Version = rustdeskVersion(ctx, bin)
	}
	return r
}

// RustDeskRunning reports whether a RustDesk process exists.
func RustDeskRunning() bool { return processRunning(processNames) }

// RustDeskID runs `rustdesk --get-id`, which asks the running RustDesk
// service. Returns "" when RustDesk is missing or does not answer.
func RustDeskID(ctx context.Context) string {
	bin := rustdeskBinary()
	if bin == "" {
		return ""
	}
	out, err := execx.Output(ctx, cmdTimeout, bin, "--get-id")
	if err != nil && len(out) == 0 {
		return ""
	}
	return parseID(string(out))
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// parseID takes the last non-empty line, so any noise printed before the ID
// is ignored.
func parseID(out string) string {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if idRe.MatchString(s) {
			return s
		}
		return ""
	}
	return ""
}

var versionRe = regexp.MustCompile(`\d+(\.\d+)+`)

func parseVersion(out string) string {
	return versionRe.FindString(out)
}

func versionFromCommand(ctx context.Context, bin string) string {
	out, err := execx.Output(ctx, cmdTimeout, bin, "--version")
	if err != nil && len(out) == 0 {
		return ""
	}
	return parseVersion(string(out))
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
