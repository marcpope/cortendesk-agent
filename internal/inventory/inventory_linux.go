package inventory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
	"github.com/shirou/gopsutil/v4/host"
)

func OS(ctx context.Context) api.OSInfo {
	o := api.OSInfo{Name: "Linux"}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		kv := ParseOSRelease(string(b))
		if v := kv["NAME"]; v != "" {
			o.Name = v
		}
		o.Version = kv["VERSION_ID"]
		o.Build = kv["BUILD_ID"]
	}
	o.Kernel, _ = host.KernelVersionWithContext(ctx)
	return o
}

func readSys(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return clean(string(b))
}

func hardware(ctx context.Context) api.Hardware {
	dmi := "/sys/class/dmi/id/"
	h := api.Hardware{
		Manufacturer: readSys(dmi + "sys_vendor"),
		Model:        readSys(dmi + "product_name"),
		Serial:       readSys(dmi + "product_serial"),
		BIOS:         readSys(dmi + "bios_version"),
	}
	// ARM boards without DMI describe themselves in the device tree.
	if h.Model == "" {
		h.Model = readSys("/proc/device-tree/model")
	}
	if h.Serial == "" {
		h.Serial = readSys("/proc/device-tree/serial-number")
	}
	h.Drives = linuxDrives()
	return h
}

var skipBlock = []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd", "nbd", "mmcblk0boot", "mmcblk0rpmb"}

func linuxDrives() []api.Drive {
	out := []api.Drive{}
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if hasAnyPrefix(name, skipBlock) {
			continue
		}
		dir := filepath.Join("/sys/block", name)
		sectors, _ := strconv.ParseUint(readSys(filepath.Join(dir, "size")), 10, 64)
		if sectors == 0 {
			continue
		}
		model := readSys(filepath.Join(dir, "device", "model"))
		if v := readSys(filepath.Join(dir, "device", "vendor")); v != "" && v != "ATA" && !strings.HasPrefix(model, v) {
			model = v + " " + model
		}
		serial := readSys(filepath.Join(dir, "device", "serial"))
		if serial == "" {
			serial = vpdSerial(filepath.Join(dir, "device", "vpd_pg80"))
		}
		// /sys/block sizes are always in 512-byte sectors.
		out = append(out, api.Drive{Model: model, Size: sectors * 512, Serial: serial})
	}
	return out
}

// vpdSerial reads a SCSI unit serial number page: 4 header bytes, then text.
func vpdSerial(p string) string {
	b, err := os.ReadFile(p)
	if err != nil || len(b) <= 4 {
		return ""
	}
	return clean(string(b[4:]))
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func software(ctx context.Context) []api.Software {
	if _, err := exec.LookPath("dpkg-query"); err == nil {
		out, err := execx.Output(ctx, cmdTimeout, "dpkg-query", "-W", "-f", "${db:Status-Abbrev}\t${Package}\t${Version}\t${Maintainer}\n")
		if err == nil {
			return ParseDpkg(string(out))
		}
	}
	if _, err := exec.LookPath("rpm"); err == nil {
		out, err := execx.Output(ctx, cmdTimeout, "rpm", "-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\t%{VENDOR}\n")
		if err == nil {
			return ParseRPM(string(out))
		}
	}
	return nil
}

func localAdmins(context.Context) []string {
	b, err := os.ReadFile("/etc/group")
	if err != nil {
		return nil
	}
	return ParseGroupMembers(string(b), "sudo", "wheel")
}

// battery reads the first system battery. Peripheral batteries (mice,
// keyboards) have scope "Device" and are skipped.
func battery(context.Context) *api.Battery {
	entries, err := os.ReadDir("/sys/class/power_supply")
	if err != nil {
		return nil
	}
	num := func(dir, name string) uint64 {
		n, _ := strconv.ParseUint(readSys(filepath.Join(dir, name)), 10, 64)
		return n
	}
	for _, e := range entries {
		dir := filepath.Join("/sys/class/power_supply", e.Name())
		if readSys(filepath.Join(dir, "type")) != "Battery" || readSys(filepath.Join(dir, "scope")) == "Device" {
			continue
		}
		b := &api.Battery{CycleCount: num(dir, "cycle_count")}
		// energy_* is in µWh; charge_* is in µAh and needs the voltage (µV).
		if d := num(dir, "energy_full_design"); d > 0 {
			b.DesignCapacity, b.FullCapacity = d/1000, num(dir, "energy_full")/1000
		} else if d := num(dir, "charge_full_design"); d > 0 {
			uv := num(dir, "voltage_min_design")
			if uv == 0 {
				continue
			}
			b.DesignCapacity = d * (uv / 1000) / 1_000_000
			b.FullCapacity = num(dir, "charge_full") * (uv / 1000) / 1_000_000
		} else {
			continue
		}
		return b
	}
	return nil
}
