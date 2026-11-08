package health

import (
	"context"
	"errors"
	"os/exec"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
)

var checks = []Check{
	{Key: "firewall", Timeout: 15 * time.Second, Run: firewall},
	{Key: "updates", Timeout: 5 * time.Minute, Run: updates},
	{Key: "smart", Timeout: 2 * time.Minute, Run: smart},
	{Key: "services", Timeout: time.Minute, Run: services},
	{Key: "wifi_ssid", Timeout: 10 * time.Second, Run: wifi},
}

var errNoTool = errors.New("tool not installed")

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// output ignores the exit status: these tools signal results through it
// (dnf check-update exits 100 when updates exist, smartctl uses a bitmask).
func output(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	b, err := execx.Output(ctx, timeout, name, args...)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return "", err
	}
	return string(b), nil
}

func firewall(ctx context.Context, _ []string, out *api.Health) error {
	if have("ufw") {
		if s, err := output(ctx, 10*time.Second, "ufw", "status"); err == nil {
			if v := ParseUFW(s); v != nil {
				out.Firewall = v
				return nil
			}
		}
	}
	if have("firewall-cmd") {
		s, err := output(ctx, 10*time.Second, "firewall-cmd", "--state")
		if err != nil {
			return err
		}
		out.Firewall = ParseFirewalld(s)
		return nil
	}
	return errNoTool
}

func updates(ctx context.Context, _ []string, out *api.Health) error {
	switch {
	case have("apt-get"):
		s, err := output(ctx, 5*time.Minute, "apt-get", "-s", "upgrade")
		if err != nil {
			return err
		}
		out.Updates = &api.Updates{Missing: ptr(ParseAptSimulate(s))}
	case have("dnf"):
		s, err := output(ctx, 5*time.Minute, "dnf", "-q", "check-update")
		if err != nil {
			return err
		}
		out.Updates = &api.Updates{Missing: ptr(ParseDnfCheckUpdate(s))}
	default:
		return errNoTool
	}
	return nil
}

func smart(ctx context.Context, _ []string, out *api.Health) error {
	if !have("smartctl") {
		return errNoTool
	}
	scan, err := output(ctx, 30*time.Second, "smartctl", "--scan")
	if err != nil {
		return err
	}
	for _, d := range ParseSmartctlScan(scan) {
		args := []string{"-H", "-i"}
		if d[1] != "" {
			args = append(args, "-d", d[1])
		}
		args = append(args, d[0])
		s, err := output(ctx, 30*time.Second, "smartctl", args...)
		if err != nil {
			continue
		}
		if r := ParseSmartctl(d[0], s); r != nil {
			out.Smart = append(out.Smart, *r)
		}
	}
	return nil
}

// "--" ends options, so a unit name is never read as a flag.
func services(ctx context.Context, watch []string, out *api.Health) error {
	if len(watch) == 0 {
		return nil
	}
	if !have("systemctl") {
		return errNoTool
	}
	for _, name := range watch {
		s, err := output(ctx, 10*time.Second, "systemctl", "show", "-p", "LoadState,ActiveState,Description", "--", name)
		if err != nil {
			return err
		}
		state, display := ParseSystemctlShow(s)
		out.Services = append(out.Services, api.Service{Name: name, Display: display, State: state})
	}
	return nil
}

func wifi(ctx context.Context, _ []string, out *api.Health) error {
	if !have("iwgetid") {
		return errNoTool
	}
	s, err := output(ctx, 10*time.Second, "iwgetid", "-r")
	if err != nil {
		return err
	}
	out.WifiSSID = trimLine(s)
	return nil
}
