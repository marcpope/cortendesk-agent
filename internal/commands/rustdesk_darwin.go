package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/execx"
	"github.com/marcpope/cortendesk-agent/internal/plist"
)

const (
	rustdeskApp       = "/Applications/RustDesk.app"
	rustdeskDaemon    = "/Library/LaunchDaemons/com.carriez.RustDesk_service.plist"
	rustdeskDaemonTgt = "system/com.carriez.RustDesk_service"
)

func rustdeskCandidates(version string) []string {
	return assetsFor(version, "rustdesk-{v}-{arch}.dmg")
}

// installRustDesk mounts the dmg, checks the app's signature (and that it
// is signed by the same team as the installed copy), then swaps the bundle
// with the RustDesk service stopped. A failed swap puts the old app back.
func installRustDesk(ctx context.Context, dmg, _ string, out *Output) error {
	mnt := dmg + ".mnt"
	if err := os.Mkdir(mnt, 0o700); err != nil {
		return err
	}
	if err := execx.Run(ctx, 5*time.Minute, out, nil, "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mnt, dmg); err != nil {
		return fmt.Errorf("mount: %w", err)
	}
	defer execx.Run(context.WithoutCancel(ctx), time.Minute, &Output{}, nil, "/usr/bin/hdiutil", "detach", mnt, "-force")

	src := filepath.Join(mnt, "RustDesk.app")
	if _, err := os.Stat(src); err != nil {
		return errors.New("the dmg has no RustDesk.app")
	}
	if err := execx.Run(ctx, 5*time.Minute, out, nil, "/usr/bin/codesign", "--verify", "--deep", "--strict", src); err != nil {
		return fmt.Errorf("signature check failed: %w", err)
	}
	if _, err := os.Stat(rustdeskApp); err == nil {
		if err := sameBundle(ctx, rustdeskApp, src); err != nil {
			return err
		}
	}

	staged := rustdeskApp + ".cortendesk-new"
	old := rustdeskApp + ".cortendesk-old"
	os.RemoveAll(staged)
	os.RemoveAll(old)
	if err := execx.Run(ctx, 10*time.Minute, out, nil, "/usr/bin/ditto", src, staged); err != nil {
		os.RemoveAll(staged)
		return fmt.Errorf("copy: %w", err)
	}
	_ = execx.Run(ctx, time.Minute, out, nil, "/usr/sbin/chown", "-R", "root:wheel", staged)

	// Stop the service so launchd doesn't restart the old binary mid-swap,
	// and quit the per-user RustDesk processes (their launch agent starts
	// them again from the new bundle).
	daemon := fileExists(rustdeskDaemon)
	if daemon {
		_ = execx.Run(ctx, time.Minute, out, nil, "/bin/launchctl", "bootout", rustdeskDaemonTgt)
	}
	_ = execx.Run(ctx, time.Minute, &Output{}, nil, "/usr/bin/pkill", "-x", "RustDesk")

	swapErr := func() error {
		if _, err := os.Stat(rustdeskApp); err == nil {
			if err := os.Rename(rustdeskApp, old); err != nil {
				return err
			}
		}
		if err := os.Rename(staged, rustdeskApp); err != nil {
			_ = os.Rename(old, rustdeskApp)
			return err
		}
		return nil
	}()
	if daemon {
		if err := execx.Run(ctx, time.Minute, out, nil, "/bin/launchctl", "bootstrap", "system", rustdeskDaemon); err != nil {
			out.Printf("Starting the RustDesk service: %v", err)
		}
	}
	os.RemoveAll(staged)
	if swapErr != nil {
		return fmt.Errorf("replace %s: %w", rustdeskApp, swapErr)
	}
	os.RemoveAll(old)
	return nil
}

var teamRe = regexp.MustCompile(`(?m)^TeamIdentifier=(\S+)`)

// sameBundle refuses a dmg whose app has another bundle id or another
// signing team than the installed RustDesk.
func sameBundle(ctx context.Context, installed, candidate string) error {
	idOf := func(app string) string {
		m, err := plist.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
		if err != nil {
			return ""
		}
		return m["CFBundleIdentifier"]
	}
	if a, b := idOf(installed), idOf(candidate); a != "" && a != b {
		return fmt.Errorf("bundle id %q does not match the installed %q", b, a)
	}
	teamOf := func(app string) string {
		var o Output
		_ = execx.Run(ctx, time.Minute, &o, nil, "/usr/bin/codesign", "-dv", "--verbose=2", app)
		if m := teamRe.FindStringSubmatch(o.String()); m != nil && m[1] != "not" {
			return m[1]
		}
		return ""
	}
	if a, b := teamOf(installed), teamOf(candidate); a != "" && a != b {
		return fmt.Errorf("signed by team %q, the installed RustDesk by %q", b, a)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
