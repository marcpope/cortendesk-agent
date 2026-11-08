package identity

import (
	"context"
	"os/exec"
)

var processNames = []string{"rustdesk"}

func rustdeskBinary() string {
	if p, err := exec.LookPath("rustdesk"); err == nil {
		return p
	}
	if fileExists("/usr/bin/rustdesk") {
		return "/usr/bin/rustdesk"
	}
	return ""
}

func rustdeskVersion(ctx context.Context, bin string) string {
	return versionFromCommand(ctx, bin)
}
