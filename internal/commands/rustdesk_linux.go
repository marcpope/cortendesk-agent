package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

// Stay with the package format RustDesk was installed with; on a machine
// without RustDesk use the native one.
func rustdeskCandidates(version string) []string {
	deb := assetsFor(version, "rustdesk-{v}-{arch}.deb")
	rpm := assetsFor(version, "rustdesk-{v}-0.{arch}.rpm")
	if isSUSE() {
		rpm = assetsFor(version, "rustdesk-{v}-0.{arch}-suse.rpm")
	}
	switch {
	case installedWith("dpkg-query", "-W", "-f=${Status}", "rustdesk"):
		return deb
	case installedWith("rpm", "-q", "rustdesk"):
		return rpm
	case has("dpkg"):
		return deb
	case has("rpm"):
		return rpm
	}
	return nil
}

func installedWith(name string, args ...string) bool {
	if !has(name) {
		return false
	}
	var o Output
	err := execx.Run(context.Background(), time.Minute, &o, nil, name, args...)
	return err == nil && !strings.Contains(o.String(), "not-installed") && !strings.Contains(o.String(), "not installed")
}

func isSUSE() bool {
	b, _ := os.ReadFile("/etc/os-release")
	return strings.Contains(strings.ToLower(string(b)), "suse")
}

func installRustDesk(ctx context.Context, path, asset string, out *Output) error {
	run := func(name string, args ...string) error {
		return execx.Run(ctx, rustdeskTimeout, out, []string{"DEBIAN_FRONTEND=noninteractive"}, name, args...)
	}
	var err error
	switch {
	case strings.HasSuffix(asset, ".deb") && has("apt-get"):
		err = run("apt-get", "install", "-y", "--allow-downgrades", path)
	case strings.HasSuffix(asset, ".deb"):
		err = run("dpkg", "-i", path)
	case has("zypper"):
		err = run("zypper", "--non-interactive", "install", "--allow-unsigned-rpm", "--oldpackage", path)
	case has("dnf"):
		// install refuses an older version; downgrade handles that case.
		if err = run("dnf", "-y", "install", path); err != nil {
			err = run("dnf", "-y", "downgrade", path)
		}
	case has("yum"):
		if err = run("yum", "-y", "install", path); err != nil {
			err = run("yum", "-y", "downgrade", path)
		}
	case has("rpm"):
		err = run("rpm", "-U", "--oldpackage", path)
	default:
		return errors.New("no package manager to install " + asset)
	}
	if err != nil {
		return err
	}
	if has("systemctl") {
		_ = run("systemctl", "restart", "rustdesk")
	}
	return nil
}
