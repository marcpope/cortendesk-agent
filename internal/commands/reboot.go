package commands

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

const (
	maxRebootDelay   = 86400
	maxRebootMessage = 200
	// minRebootDelay leaves time to send the result before the machine goes
	// down.
	minRebootDelay = 15 * time.Second
)

type rebootParams struct {
	Delay   int    `json:"delay"`
	Message string `json:"message"`
}

func parseReboot(params json.RawMessage) (time.Duration, string, error) {
	var p rebootParams
	if err := decode(params, &p); err != nil {
		return 0, "", err
	}
	if p.Delay < 0 || p.Delay > maxRebootDelay {
		return 0, "", errors.New("delay must be 0 to 86400 seconds")
	}
	return max(time.Duration(p.Delay)*time.Second, minRebootDelay), cleanMessage(p.Message), nil
}

// cleanMessage keeps printable characters only and drops quotes, since the
// message ends up on a command line (as one argument, never through a
// shell).
func cleanMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) || r == '"' || r == '`' {
			return -1
		}
		return r
	}, s)
	if r := []rune(strings.TrimSpace(s)); len(r) > maxRebootMessage {
		s = string(r[:maxRebootMessage])
	}
	return strings.TrimSpace(s)
}

func reboot(ctx context.Context, params json.RawMessage, out *Output) error {
	delay, msg, err := parseReboot(params)
	if err != nil {
		return err
	}
	return scheduleReboot(ctx, delay, msg, out)
}
