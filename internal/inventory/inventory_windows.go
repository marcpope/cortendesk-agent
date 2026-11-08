package inventory

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func openLM(path string, extra uint32) (registry.Key, error) {
	return registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS|extra)
}

func str(k registry.Key, name string) string {
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func num(k registry.Key, name string) (uint64, bool) {
	v, _, err := k.GetIntegerValue(name)
	return v, err == nil
}

func OS(context.Context) api.OSInfo {
	o := api.OSInfo{Name: "Windows"}
	k, err := openLM(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.WOW64_64KEY)
	if err != nil {
		return o
	}
	defer k.Close()
	if v := str(k, "ProductName"); v != "" {
		o.Name = v
	}
	o.Version = str(k, "DisplayVersion")
	if o.Version == "" {
		o.Version = str(k, "ReleaseId")
	}
	build := str(k, "CurrentBuildNumber")
	if build == "" {
		build = str(k, "CurrentBuild")
	}
	o.Build = build
	if ubr, ok := num(k, "UBR"); ok && build != "" {
		o.Build = fmt.Sprintf("%s.%d", build, ubr)
	}
	major, ok1 := num(k, "CurrentMajorVersionNumber")
	minor, ok2 := num(k, "CurrentMinorVersionNumber")
	if ok1 && ok2 {
		o.Kernel = fmt.Sprintf("%d.%d.%s", major, minor, build)
	} else if v := str(k, "CurrentVersion"); v != "" {
		o.Kernel = v + "." + build
	}
	// Windows 11 still says "Windows 10" in ProductName.
	if n, err := strconv.Atoi(build); err == nil && n >= 22000 && str(k, "InstallationType") == "Client" {
		o.Name = strings.Replace(o.Name, "Windows 10", "Windows 11", 1)
	}
	return o
}

// One PowerShell call for the facts the registry does not have (serial,
// physical drives). Read once a day, so the startup cost does not matter.
const hwScript = `$cs=Get-CimInstance Win32_ComputerSystem;$b=Get-CimInstance Win32_BIOS;` +
	`$d=@(Get-CimInstance Win32_DiskDrive|ForEach-Object{[pscustomobject]@{model=[string]$_.Model;size=[uint64]$_.Size;serial=[string]$_.SerialNumber}});` +
	`[pscustomobject]@{manufacturer=[string]$cs.Manufacturer;model=[string]$cs.Model;serial=[string]$b.SerialNumber;bios=[string]$b.SMBIOSBIOSVersion;drives=$d}|ConvertTo-Json -Compress -Depth 3`

func hardware(ctx context.Context) api.Hardware {
	h := api.Hardware{Drives: []api.Drive{}}
	var r struct {
		Manufacturer string      `json:"manufacturer"`
		Model        string      `json:"model"`
		Serial       string      `json:"serial"`
		BIOS         string      `json:"bios"`
		Drives       []api.Drive `json:"drives"`
	}
	if execx.PowerShellJSON(ctx, cmdTimeout, hwScript, &r) == nil {
		h.Manufacturer, h.Model, h.Serial, h.BIOS = clean(r.Manufacturer), clean(r.Model), clean(r.Serial), clean(r.BIOS)
		for _, d := range r.Drives {
			h.Drives = append(h.Drives, api.Drive{Model: clean(d.Model), Size: d.Size, Serial: clean(d.Serial)})
		}
	}
	// Registry fallback when PowerShell is blocked or slow.
	if h.Manufacturer == "" || h.Model == "" || h.BIOS == "" {
		if k, err := openLM(`HARDWARE\DESCRIPTION\System\BIOS`, 0); err == nil {
			if h.Manufacturer == "" {
				h.Manufacturer = str(k, "SystemManufacturer")
			}
			if h.Model == "" {
				h.Model = str(k, "SystemProductName")
			}
			if h.BIOS == "" {
				h.BIOS = str(k, "BIOSVersion")
			}
			k.Close()
		}
	}
	return h
}

const uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// software reads the machine-wide uninstall entries from both registry
// views, skipping system components and updates the way Programs and
// Features does.
func software(context.Context) []api.Software {
	var out []api.Software
	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		root, err := openLM(uninstallKey, view)
		if err != nil {
			continue
		}
		names, _ := root.ReadSubKeyNames(-1)
		root.Close()
		for _, n := range names {
			k, err := openLM(uninstallKey+`\`+n, view)
			if err != nil {
				continue
			}
			name := str(k, "DisplayName")
			sys, _ := num(k, "SystemComponent")
			rt := str(k, "ReleaseType")
			skip := name == "" || sys == 1 || str(k, "ParentKeyName") != "" ||
				rt == "Update" || rt == "Hotfix" || rt == "Security Update"
			if !skip {
				out = append(out, api.Software{Name: name, Version: str(k, "DisplayVersion"), Publisher: str(k, "Publisher")})
			}
			k.Close()
		}
	}
	return out
}

var (
	netapi32                    = windows.NewLazySystemDLL("netapi32.dll")
	procNetLocalGroupGetMembers = netapi32.NewProc("NetLocalGroupGetMembers")
)

// localAdmins lists the Administrators group, found by its well-known SID
// because the group name is localized. Local accounts lose the computer
// name prefix; domain accounts keep DOMAIN\.
func localAdmins(context.Context) []string {
	sid, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return nil
	}
	group, _, _, err := sid.LookupAccount("")
	if err != nil {
		return nil
	}
	g, err := windows.UTF16PtrFromString(group)
	if err != nil {
		return nil
	}
	var buf *byte
	var read, total uint32
	r, _, _ := procNetLocalGroupGetMembers.Call(0, uintptr(unsafe.Pointer(g)), 3,
		uintptr(unsafe.Pointer(&buf)), 0xFFFFFFFF, uintptr(unsafe.Pointer(&read)), uintptr(unsafe.Pointer(&total)), 0)
	if r != 0 || buf == nil {
		return nil
	}
	defer windows.NetApiBufferFree(buf)
	host, _ := windows.ComputerName()
	var out []string
	// LOCALGROUP_MEMBERS_INFO_3 is a single LPWSTR.
	for _, p := range unsafe.Slice((**uint16)(unsafe.Pointer(buf)), read) {
		name := windows.UTF16PtrToString(p)
		if d, n, ok := strings.Cut(name, `\`); ok && strings.EqualFold(d, host) {
			name = n
		}
		out = append(out, name)
	}
	return out
}

// Battery classes in root/wmi report mWh. Desktops have none.
const batteryScript = `$s=Get-CimInstance -Namespace root/wmi -ClassName BatteryStaticData | Select-Object -First 1;` +
	`if(-not $s){'null';exit 0};` +
	`$f=Get-CimInstance -Namespace root/wmi -ClassName BatteryFullChargedCapacity | Select-Object -First 1;` +
	`$c=Get-CimInstance -Namespace root/wmi -ClassName BatteryCycleCount | Select-Object -First 1;` +
	`[pscustomobject]@{design_capacity=[uint64]$s.DesignedCapacity;full_capacity=[uint64]$f.FullChargedCapacity;cycle_count=[uint64]$c.CycleCount} | ConvertTo-Json -Compress`

func battery(ctx context.Context) *api.Battery {
	var b *api.Battery
	if execx.PowerShellJSON(ctx, cmdTimeout, batteryScript, &b) != nil || b == nil || b.DesignCapacity == 0 {
		return nil
	}
	return b
}
