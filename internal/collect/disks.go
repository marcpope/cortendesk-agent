package collect

import (
	"runtime"
	"slices"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

var goos = runtime.GOOS

const hasLoadAvg = runtime.GOOS != "windows"

// Pseudo, memory and image filesystems. None of them is a disk anyone wants
// a "disk full" alert for.
var skipFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "devfs": true, "overlay": true, "squashfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "autofs": true,
	"mqueue": true, "debugfs": true, "tracefs": true, "securityfs": true, "pstore": true,
	"bpf": true, "configfs": true, "fusectl": true, "hugetlbfs": true, "binfmt_misc": true,
	"nsfs": true, "ramfs": true, "rpc_pipefs": true, "efivarfs": true, "fuse.lxcfs": true,
	"fuse.portal": true, "fuse.gvfsd-fuse": true, "devpts": true, "iso9660": true,
	"udf": true, "cdfs": true, "nullfs": true, "devicefs": true, "erofs": true,
}

// FilterPartitions keeps one entry per real filesystem.
//
// macOS: the sealed system volume "/" and /System/Volumes/Data share one APFS
// container, and statfs on "/" already reports the container's total and
// used space. So "/" is kept and every nobrowse volume (Data, VM, Preboot,
// Recovery...) is dropped. Read-only mounts other than "/" are mostly
// mounted disk images and are dropped too.
func FilterPartitions(parts []disk.PartitionStat, goos string) []disk.PartitionStat {
	out := []disk.PartitionStat{}
	seen := map[string]int{} // device -> index in out
	for _, p := range parts {
		if p.Mountpoint == "" || skipFS[strings.ToLower(p.Fstype)] {
			continue
		}
		switch goos {
		case "darwin":
			if slices.Contains(p.Opts, "nobrowse") {
				continue
			}
			if p.Mountpoint != "/" && slices.Contains(p.Opts, "ro") {
				continue
			}
		case "linux":
			if strings.HasPrefix(p.Device, "/dev/loop") || strings.HasPrefix(p.Mountpoint, "/snap/") ||
				underAny(p.Mountpoint, "/proc", "/sys", "/dev", "/run", "/var/lib/docker", "/var/lib/containers") {
				continue
			}
		}
		// Bind mounts and btrfs subvolumes repeat a device. Keep the
		// shortest mount path.
		if p.Device != "" && goos != "windows" {
			if i, ok := seen[p.Device]; ok {
				if len(p.Mountpoint) < len(out[i].Mountpoint) {
					out[i] = p
				}
				continue
			}
			seen[p.Device] = len(out)
		}
		out = append(out, p)
	}
	return out
}

func underAny(path string, roots ...string) bool {
	for _, r := range roots {
		if path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}
