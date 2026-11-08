package health

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
)

var checks = []Check{
	{Key: "antivirus", Timeout: 5 * time.Second, Run: antivirus},
	{Key: "firewall", Timeout: 15 * time.Second, Run: firewall},
	{Key: "encryption", Timeout: 15 * time.Second, Run: encryption},
	{Key: "smart", Timeout: time.Minute, Run: smart},
	{Key: "services", Timeout: time.Minute, Run: services},
	{Key: "wifi_ssid", Timeout: 15 * time.Second, Run: wifi},
}

var xprotectPaths = []string{
	"/Library/Apple/System/Library/CoreServices/XProtect.bundle",
	"/Library/Apple/System/Library/CoreServices/XProtect.app",
	"/System/Library/CoreServices/XProtect.bundle",
}

// XProtect is always on and updates itself; report it by name only.
func antivirus(_ context.Context, _ []string, out *api.Health) error {
	for _, p := range xprotectPaths {
		if _, err := os.Stat(p); err == nil {
			out.Antivirus = []api.Antivirus{{Name: "XProtect"}}
			return nil
		}
	}
	return nil
}

func firewall(ctx context.Context, _ []string, out *api.Health) error {
	b, err := execx.Output(ctx, 15*time.Second, "/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	if err != nil {
		return err
	}
	out.Firewall = ParseSocketFilterFW(string(b))
	return nil
}

func encryption(ctx context.Context, _ []string, out *api.Health) error {
	b, err := execx.Output(ctx, 15*time.Second, "/usr/bin/fdesetup", "status")
	if err != nil {
		return err
	}
	if e := ParseFdesetup(string(b)); e != nil {
		out.Encryption = []api.Encryption{*e}
	}
	return nil
}

func smart(ctx context.Context, _ []string, out *api.Health) error {
	b, err := execx.Output(ctx, 15*time.Second, "/usr/sbin/diskutil", "list", "physical")
	if err != nil {
		return err
	}
	for _, d := range ParseDiskutilList(string(b)) {
		info, err := execx.Output(ctx, 15*time.Second, "/usr/sbin/diskutil", "info", d)
		if err != nil {
			continue
		}
		if s := ParseDiskutilSmart(d, string(info)); s != nil {
			out.Smart = append(out.Smart, *s)
		}
	}
	return nil
}

// Each label is its own argv element; launchctl never sees a shell.
func services(ctx context.Context, watch []string, out *api.Health) error {
	for _, name := range watch {
		b, err := execx.Output(ctx, 10*time.Second, "/bin/launchctl", "print", "system/"+name)
		var ee *exec.ExitError
		var state string
		switch {
		case err == nil:
			state = ParseLaunchctlState(string(b))
		case errors.As(err, &ee):
			state = api.ServiceMissing // exit 113: no such service
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			state = api.ServiceOther
		}
		out.Services = append(out.Services, api.Service{Name: name, State: state})
	}
	return nil
}

func wifi(ctx context.Context, _ []string, out *api.Health) error {
	dev := "en0"
	if b, err := execx.Output(ctx, 10*time.Second, "/usr/sbin/networksetup", "-listallhardwareports"); err == nil {
		if d := ParseWifiDevice(string(b)); d != "" {
			dev = d
		}
	}
	b, err := execx.Output(ctx, 10*time.Second, "/usr/sbin/ipconfig", "getsummary", dev)
	if err != nil {
		return err
	}
	out.WifiSSID = ParseIPConfigSSID(string(b))
	return nil
}
