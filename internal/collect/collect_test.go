package collect

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
)

func mounts(parts []disk.PartitionStat) []string {
	var m []string
	for _, p := range parts {
		m = append(m, p.Mountpoint)
	}
	return m
}

func TestFilterDarwin(t *testing.T) {
	in := []disk.PartitionStat{
		{Device: "/dev/disk3s1s1", Mountpoint: "/", Fstype: "apfs", Opts: []string{"ro", "journaled", "local"}},
		{Device: "devfs", Mountpoint: "/dev", Fstype: "devfs", Opts: []string{"rw", "nobrowse"}},
		{Device: "/dev/disk3s6", Mountpoint: "/System/Volumes/VM", Fstype: "apfs", Opts: []string{"rw", "nobrowse"}},
		{Device: "/dev/disk3s5", Mountpoint: "/System/Volumes/Data", Fstype: "apfs", Opts: []string{"rw", "nobrowse"}},
		{Device: "map auto_home", Mountpoint: "/System/Volumes/Data/home", Fstype: "autofs", Opts: []string{"rw", "nobrowse"}},
		{Device: "/dev/disk7s1", Mountpoint: "/Volumes/Projects", Fstype: "apfs", Opts: []string{"rw", "local"}},
		{Device: "/dev/disk9s1", Mountpoint: "/Volumes/Some Installer", Fstype: "hfs", Opts: []string{"ro", "local"}},
		{Device: "/dev/disk3s3", Mountpoint: "/Volumes/Recovery", Fstype: "apfs", Opts: []string{"rw", "nobrowse"}},
	}
	got := mounts(FilterPartitions(in, "darwin"))
	want := []string{"/", "/Volumes/Projects"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilterLinux(t *testing.T) {
	in := []disk.PartitionStat{
		{Device: "/dev/sda1", Mountpoint: "/", Fstype: "ext4"},
		{Device: "/dev/sda1", Mountpoint: "/var/lib/docker/overlay2/x", Fstype: "ext4"},
		{Device: "/dev/sda1", Mountpoint: "/srv/bind", Fstype: "ext4"},
		{Device: "/dev/sda2", Mountpoint: "/boot/efi", Fstype: "vfat"},
		{Device: "/dev/loop3", Mountpoint: "/snap/core/123", Fstype: "squashfs"},
		{Device: "tmpfs", Mountpoint: "/run", Fstype: "tmpfs"},
		{Device: "overlay", Mountpoint: "/merged", Fstype: "overlay"},
		{Device: "/dev/nvme0n1p1", Mountpoint: "/data", Fstype: "xfs"},
	}
	got := mounts(FilterPartitions(in, "linux"))
	want := []string{"/", "/boot/efi", "/data"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFilterWindows(t *testing.T) {
	in := []disk.PartitionStat{
		{Device: "C:", Mountpoint: "C:", Fstype: "NTFS"},
		{Device: "D:", Mountpoint: "D:", Fstype: "CDFS"},
		{Device: "E:", Mountpoint: "E:", Fstype: "ReFS"},
	}
	got := mounts(FilterPartitions(in, "windows"))
	if len(got) != 2 || got[0] != "C:" || got[1] != "E:" {
		t.Fatalf("got %v", got)
	}
}

func TestBusyPercent(t *testing.T) {
	a := &cpu.TimesStat{User: 100, System: 50, Idle: 850}
	b := &cpu.TimesStat{User: 120, System: 60, Idle: 920}
	// 30 busy of 100 elapsed.
	if got := busyPercent(a, b); got != 30 {
		t.Fatalf("got %v", got)
	}
	if got := busyPercent(a, a); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestIsLoopbackName(t *testing.T) {
	for _, n := range []string{"lo", "lo0", "Loopback Pseudo-Interface 1"} {
		if !IsLoopbackName(n, nil) {
			t.Errorf("%s should be loopback", n)
		}
	}
	if IsLoopbackName("en0", nil) || IsLoopbackName("Ethernet0", nil) {
		t.Error("real interface marked loopback")
	}
}
