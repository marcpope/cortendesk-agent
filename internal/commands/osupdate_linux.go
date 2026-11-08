package commands

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

func installUpdates(ctx context.Context, out *Output) (bool, error) {
	run := func(name string, args ...string) error {
		return execx.Run(ctx, osUpdateTimeout, out, []string{"DEBIAN_FRONTEND=noninteractive"}, name, args...)
	}
	switch {
	case has("apt-get"):
		if err := run("apt-get", "update"); err != nil {
			return false, err
		}
		// Keep changed config files; never ask.
		if err := run("apt-get", "-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade"); err != nil {
			return false, err
		}
		_, err := os.Stat("/var/run/reboot-required")
		return err == nil, nil
	case has("dnf"):
		if err := run("dnf", "-y", "upgrade"); err != nil {
			return false, err
		}
		return needsRestarting(ctx), nil
	case has("yum"):
		if err := run("yum", "-y", "update"); err != nil {
			return false, err
		}
		return needsRestarting(ctx), nil
	case has("zypper"):
		if err := run("zypper", "--non-interactive", "update"); err != nil {
			return false, err
		}
		// zypper needs-rebooting exits 102 when a reboot is needed.
		err := execx.Run(ctx, osUpdateTimeout, out, nil, "zypper", "needs-rebooting")
		var ee *exec.ExitError
		return errors.As(err, &ee) && ee.ExitCode() == 102, nil
	}
	return false, errors.New("no supported package manager (apt-get, dnf, yum, zypper)")
}

// needsRestarting asks dnf-utils; exit 1 means a reboot is needed. Missing
// tool means unknown, read as no.
func needsRestarting(ctx context.Context) bool {
	if !has("needs-restarting") {
		return false
	}
	err := execx.Run(ctx, osUpdateTimeout, &Output{}, nil, "needs-restarting", "-r")
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 1
}

func has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
