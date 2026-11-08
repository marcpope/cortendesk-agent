package health

import (
	"bufio"
	"regexp"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

// Parsers for command output. They live outside the OS files so their tests
// run everywhere.

func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		out = append(out, strings.TrimRight(sc.Text(), "\r"))
	}
	return out
}

// field returns the value after "label :" on the first matching line.
func field(s, label string) (string, bool) {
	for _, l := range lines(s) {
		k, v, ok := strings.Cut(l, ":")
		if ok && strings.TrimSpace(k) == label {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// ParseNetshSSID reads `netsh wlan show interfaces`. The "SSID" line is
// matched exactly so "BSSID" is not picked up.
func ParseNetshSSID(out string) string {
	v, _ := field(out, "SSID")
	return v
}

// ParseIPConfigSSID reads `ipconfig getsummary <if>` on macOS. Recent macOS
// prints <redacted> without location access; that counts as unknown.
func ParseIPConfigSSID(out string) string {
	v, _ := field(out, "SSID")
	if strings.HasPrefix(v, "<") {
		return ""
	}
	return v
}

// ParseWifiDevice finds the Wi-Fi device in
// `networksetup -listallhardwareports`.
func ParseWifiDevice(out string) string {
	ls := lines(out)
	for i, l := range ls {
		if v, ok := strings.CutPrefix(l, "Hardware Port: "); ok && (v == "Wi-Fi" || v == "AirPort") {
			for _, n := range ls[i+1:] {
				if d, ok := strings.CutPrefix(n, "Device: "); ok {
					return strings.TrimSpace(d)
				}
			}
		}
	}
	return ""
}

// ParseSocketFilterFW reads `socketfilterfw --getglobalstate`.
func ParseSocketFilterFW(out string) *bool {
	s := strings.ToLower(out)
	switch {
	case strings.Contains(s, "disabled"):
		return ptr(false)
	case strings.Contains(s, "enabled") || strings.Contains(s, "block"):
		return ptr(true)
	}
	return nil
}

// ParseFdesetup reads `fdesetup status`.
func ParseFdesetup(out string) *api.Encryption {
	s := strings.ToLower(out)
	e := &api.Encryption{Volume: "/"}
	switch {
	case strings.Contains(s, "filevault is on"), strings.Contains(s, "encryption in progress"):
		e.Encrypted, e.Method = true, "FileVault"
	case strings.Contains(s, "filevault is off"), strings.Contains(s, "decryption in progress"):
	default:
		return nil
	}
	return e
}

var physDiskRe = regexp.MustCompile(`^/dev/(disk\d+) \(.*physical`)

// ParseDiskutilList returns the physical whole disks from
// `diskutil list physical`.
func ParseDiskutilList(out string) []string {
	var d []string
	for _, l := range lines(out) {
		if m := physDiskRe.FindStringSubmatch(l); m != nil {
			d = append(d, m[1])
		}
	}
	return d
}

// ParseDiskutilSmart reads `diskutil info diskN`. Disks that do not
// support SMART return nil.
func ParseDiskutilSmart(dev, out string) *api.Smart {
	status, ok := field(out, "SMART Status")
	if !ok || status == "" || strings.EqualFold(status, "Not Supported") {
		return nil
	}
	model, _ := field(out, "Device / Media Name")
	return &api.Smart{Device: "/dev/" + dev, Model: model, Healthy: strings.EqualFold(status, "Verified"), Status: status}
}

// ParseLaunchctlState reads the top-level "state = ..." of
// `launchctl print system/<label>`. Nested blocks are indented further.
func ParseLaunchctlState(out string) string {
	for _, l := range lines(out) {
		if v, ok := strings.CutPrefix(l, "\tstate = "); ok {
			switch strings.TrimSpace(v) {
			case "running":
				return api.ServiceRunning
			case "not running", "waiting", "exited":
				return api.ServiceStopped
			}
			return api.ServiceOther
		}
	}
	return api.ServiceOther
}

// ParseSystemctlShow reads `systemctl show -p LoadState,ActiveState,Description`.
func ParseSystemctlShow(out string) (state, display string) {
	kv := map[string]string{}
	for _, l := range lines(out) {
		if k, v, ok := strings.Cut(l, "="); ok {
			kv[k] = v
		}
	}
	display = kv["Description"]
	switch {
	case kv["LoadState"] == "not-found":
		return api.ServiceMissing, ""
	case kv["ActiveState"] == "active" || kv["ActiveState"] == "reloading":
		return api.ServiceRunning, display
	case kv["ActiveState"] == "inactive" || kv["ActiveState"] == "failed":
		return api.ServiceStopped, display
	}
	return api.ServiceOther, display
}

// ParseUFW reads `ufw status`.
func ParseUFW(out string) *bool {
	v, ok := field(out, "Status")
	if !ok {
		return nil
	}
	switch v {
	case "active":
		return ptr(true)
	case "inactive":
		return ptr(false)
	}
	return nil
}

// ParseFirewalld reads `firewall-cmd --state`.
func ParseFirewalld(out string) *bool {
	switch strings.TrimSpace(out) {
	case "running":
		return ptr(true)
	case "not running":
		return ptr(false)
	}
	return nil
}

// ParseAptSimulate counts "Inst" lines of `apt-get -s upgrade`.
func ParseAptSimulate(out string) int {
	n := 0
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "Inst ") {
			n++
		}
	}
	return n
}

// ParseDnfCheckUpdate counts package lines of `dnf -q check-update`,
// stopping at the obsoletes section.
func ParseDnfCheckUpdate(out string) int {
	n := 0
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "Obsoleting") {
			break
		}
		f := strings.Fields(l)
		if len(f) == 3 && strings.Contains(f[0], ".") && !strings.HasPrefix(l, " ") {
			n++
		}
	}
	return n
}

// ParseSmartctlScan returns device and -d type pairs from `smartctl --scan`.
func ParseSmartctlScan(out string) [][2]string {
	var d [][2]string
	for _, l := range lines(out) {
		l, _, _ = strings.Cut(l, "#")
		f := strings.Fields(l)
		if len(f) == 0 || !strings.HasPrefix(f[0], "/dev/") {
			continue
		}
		typ := ""
		if len(f) >= 3 && f[1] == "-d" {
			typ = f[2]
		}
		d = append(d, [2]string{f[0], typ})
	}
	return d
}

// ParseSmartctl reads `smartctl -H -i`. ATA/NVMe print "overall-health",
// SCSI prints "SMART Health Status".
func ParseSmartctl(dev, out string) *api.Smart {
	s := &api.Smart{Device: dev}
	for _, k := range []string{"Device Model", "Model Number", "Product"} {
		if v, ok := field(out, k); ok && v != "" {
			s.Model = v
			break
		}
	}
	if v, ok := field(out, "SMART overall-health self-assessment test result"); ok {
		s.Status, s.Healthy = v, v == "PASSED"
		return s
	}
	if v, ok := field(out, "SMART Health Status"); ok {
		s.Status, s.Healthy = v, v == "OK"
		return s
	}
	return nil
}
