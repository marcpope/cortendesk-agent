package inventory

import (
	"bufio"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// Parsers for command output live here, outside the OS files, so their tests
// run on every OS.

func parseColonPairs(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

type spDrive struct {
	Name   string `json:"_name"`
	Model  string `json:"device_model"`
	Serial string `json:"device_serial"`
	Size   uint64 `json:"size_in_bytes"`
}

type spController struct {
	Items []spDrive `json:"_items"`
}

type spReport struct {
	Hardware []struct {
		MachineName  string `json:"machine_name"`
		MachineModel string `json:"machine_model"`
		Serial       string `json:"serial_number"`
		BootROM      string `json:"boot_rom_version"`
	} `json:"SPHardwareDataType"`
	NVMe []spController `json:"SPNVMeDataType"`
	SATA []spController `json:"SPSerialATADataType"`
}

func parseSystemProfiler(out []byte, h api.Hardware) api.Hardware {
	var r spReport
	if json.Unmarshal(out, &r) != nil {
		return h
	}
	if len(r.Hardware) > 0 {
		hw := r.Hardware[0]
		h.Model = hw.MachineName
		if hw.MachineModel != "" {
			if h.Model != "" {
				h.Model += " (" + hw.MachineModel + ")"
			} else {
				h.Model = hw.MachineModel
			}
		}
		h.Serial = hw.Serial
		h.BIOS = hw.BootROM
	}
	for _, c := range append(r.NVMe, r.SATA...) {
		for _, d := range c.Items {
			model := d.Model
			if model == "" {
				model = d.Name
			}
			h.Drives = append(h.Drives, api.Drive{Model: clean(model), Size: d.Size, Serial: clean(d.Serial)})
		}
	}
	return h
}

// ParseOSRelease reads KEY=value lines, values optionally quoted.
func ParseOSRelease(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `'"`)
		}
		m[k] = v
	}
	return m
}

// ParseDpkg keeps installed packages only ("ii" status).
func ParseDpkg(s string) []api.Software {
	var out []api.Software
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 4 || !strings.HasPrefix(f[0], "ii") {
			continue
		}
		out = append(out, api.Software{Name: f[1], Version: f[2], Publisher: f[3]})
	}
	return out
}

func ParseRPM(s string) []api.Software {
	var out []api.Software
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 3 || f[0] == "gpg-pubkey" {
			continue
		}
		pub := f[2]
		if pub == "(none)" {
			pub = ""
		}
		out = append(out, api.Software{Name: f[0], Version: f[1], Publisher: pub})
	}
	return out
}

// ParseDsclMembers reads `dscl . -read /Groups/admin GroupMembership`.
func ParseDsclMembers(s string) []string {
	_, v, ok := strings.Cut(s, "GroupMembership:")
	if !ok {
		return nil
	}
	v, _, _ = strings.Cut(v, "\n")
	return strings.Fields(v)
}

// ParseGroupMembers returns the members of the named groups from an
// /etc/group file.
func ParseGroupMembers(s string, groups ...string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 4 || !slices.Contains(groups, f[0]) {
			continue
		}
		for _, m := range strings.Split(f[3], ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
	}
	return out
}

// IORegNumber reads an integer property from `ioreg` text output:
//
//	"CycleCount" = 212
func IORegNumber(out, key string) (uint64, bool) {
	needle := `"` + key + `" = `
	i := strings.Index(out, needle)
	if i < 0 {
		return 0, false
	}
	rest := out[i+len(needle):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	n, err := strconv.ParseUint(rest[:j], 10, 64)
	return n, err == nil
}

// MacBattery builds the battery entry from `ioreg -rn AppleSmartBattery`.
// macOS reports capacities in mAh; they are turned into mWh with the
// current pack voltage (mV), so the result is approximate.
func MacBattery(out string) *api.Battery {
	if !strings.Contains(out, `"BatteryInstalled" = Yes`) {
		return nil
	}
	design, ok1 := IORegNumber(out, "DesignCapacity")
	full, ok2 := IORegNumber(out, "AppleRawMaxCapacity")
	mv, ok3 := IORegNumber(out, "Voltage")
	cycles, _ := IORegNumber(out, "CycleCount")
	if !ok1 || !ok2 || !ok3 || design == 0 || mv == 0 {
		return nil
	}
	return &api.Battery{DesignCapacity: design * mv / 1000, FullCapacity: full * mv / 1000, CycleCount: cycles}
}
