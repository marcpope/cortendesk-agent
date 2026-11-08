package identity

import (
	"context"

	"github.com/marcpope/cortendesk-agent/internal/plist"
)

const rustdeskApp = "/Applications/RustDesk.app"

var processNames = []string{"rustdesk"}

func rustdeskBinary() string {
	if p := rustdeskApp + "/Contents/MacOS/RustDesk"; fileExists(p) {
		return p
	}
	return ""
}

// The bundle's Info.plist is cheaper and safer than launching the app.
func rustdeskVersion(ctx context.Context, bin string) string {
	if m, err := plist.ReadFile(rustdeskApp + "/Contents/Info.plist"); err == nil {
		if v := plist.BundleVersion(m); v != "" {
			return v
		}
	}
	return versionFromCommand(ctx, bin)
}
