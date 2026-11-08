package inventory

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
	"github.com/marcpope/cortendesk-agent/internal/plist"
	"github.com/shirou/gopsutil/v4/host"
)

func OS(ctx context.Context) api.OSInfo {
	o := api.OSInfo{Name: "macOS"}
	if out, err := execx.Output(ctx, cmdTimeout, "/usr/bin/sw_vers"); err == nil {
		kv := parseColonPairs(string(out))
		if v := kv["ProductName"]; v != "" {
			o.Name = v
		}
		o.Version = kv["ProductVersion"]
		o.Build = kv["BuildVersion"]
	}
	o.Kernel, _ = host.KernelVersionWithContext(ctx)
	return o
}

func hardware(ctx context.Context) api.Hardware {
	h := api.Hardware{Manufacturer: "Apple Inc.", Drives: []api.Drive{}}
	out, err := execx.Output(ctx, cmdTimeout, "/usr/sbin/system_profiler", "-json",
		"SPHardwareDataType", "SPNVMeDataType", "SPSerialATADataType")
	if err != nil {
		return h
	}
	return parseSystemProfiler(out, h)
}

// Bundles under these roots, at most this many levels down. Bundles are not
// searched inside.
var appRoots = []struct {
	dir   string
	depth int
}{
	{"/Applications", 2},
	{"/Library", 3},
}

func software(ctx context.Context) []api.Software {
	var out []api.Software
	for _, r := range appRoots {
		out = append(out, scanBundles(ctx, r.dir, r.depth)...)
	}
	return out
}

func scanBundles(ctx context.Context, root string, maxDepth int) []api.Software {
	var out []api.Software
	base := strings.Count(root, string(os.PathSeparator))
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".app") {
			name := strings.TrimSuffix(filepath.Base(p), ".app")
			m, err := plist.ReadFile(filepath.Join(p, "Contents", "Info.plist"))
			if err == nil {
				out = append(out, api.Software{Name: plist.BundleName(m, name), Version: plist.BundleVersion(m)})
			}
			return fs.SkipDir
		}
		if strings.Count(p, string(os.PathSeparator))-base >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	return out
}

func localAdmins(ctx context.Context) []string {
	out, err := execx.Output(ctx, cmdTimeout, "/usr/bin/dscl", ".", "-read", "/Groups/admin", "GroupMembership")
	if err != nil {
		return nil
	}
	return ParseDsclMembers(string(out))
}

func battery(ctx context.Context) *api.Battery {
	out, err := execx.Output(ctx, cmdTimeout, "/usr/sbin/ioreg", "-rn", "AppleSmartBattery")
	if err != nil {
		return nil
	}
	return MacBattery(string(out))
}
