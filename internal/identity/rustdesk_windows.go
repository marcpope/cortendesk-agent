package identity

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

var processNames = []string{"rustdesk.exe"}

func rustdeskBinary() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	if p := filepath.Join(pf, "RustDesk", "rustdesk.exe"); fileExists(p) {
		return p
	}
	return ""
}

// The installer writes DisplayVersion under its uninstall key. Fall back to
// asking the binary.
func rustdeskVersion(ctx context.Context, bin string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\RustDesk`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err == nil {
		v, _, err := k.GetStringValue("DisplayVersion")
		k.Close()
		if err == nil && v != "" {
			return v
		}
	}
	return versionFromCommand(ctx, bin)
}
