// Package inventory collects the slow-changing facts about a machine: OS,
// hardware, network interfaces and installed software.
package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// MaxSoftware caps the software list, per the console contract.
const MaxSoftware = 5000

const cmdTimeout = 60 * time.Second

// Collect gathers a full inventory and sets its hash. Parts that fail are
// left empty.
func Collect(ctx context.Context) api.Inventory {
	inv := api.Inventory{
		OS:          OS(ctx),
		Hardware:    hardware(ctx),
		Network:     Interfaces(),
		Software:    Normalize(software(ctx)),
		LocalAdmins: uniqueSorted(localAdmins(ctx)),
		Battery:     battery(ctx),
	}
	h := &inv.Hardware
	if h.Drives == nil {
		h.Drives = []api.Drive{}
	}
	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		h.CPU = strings.TrimSpace(infos[0].ModelName)
	}
	h.Cores, _ = cpu.CountsWithContext(ctx, false)
	h.Threads, _ = cpu.CountsWithContext(ctx, true)
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.Memory = vm.Total
	}
	inv.Hash = Hash(inv)
	return inv
}

// Hash is sha256 (hex) of the canonical JSON of the inventory without the
// hash field. Canonical here means object keys sorted at every level, no
// whitespace and no HTML escaping, so the console can recompute it from the
// body it received. Lists are sorted before this, so the same machine gives
// the same hash.
func Hash(inv api.Inventory) string {
	inv.Hash = ""
	b, err := json.Marshal(inv)
	if err != nil {
		return ""
	}
	c, err := Canonical(b)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:])
}

// Canonical re-encodes JSON with sorted keys (Go sorts map keys) and no
// whitespace. Numbers keep their original text.
func Canonical(b []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// OSVersion is the one-line form sent at enroll, e.g.
// "Windows Server 2022 Standard 21H2 (20348.2700)".
func OSVersion(o api.OSInfo) string {
	s := strings.TrimSpace(o.Name + " " + o.Version)
	if o.Build != "" {
		s += " (" + o.Build + ")"
	}
	return s
}

// Normalize trims, drops nameless and duplicate entries, sorts by name and
// applies the cap.
func Normalize(in []api.Software) []api.Software {
	out := make([]api.Software, 0, len(in))
	seen := map[api.Software]bool{}
	for _, s := range in {
		s.Name = strings.TrimSpace(s.Name)
		s.Version = strings.TrimSpace(s.Version)
		s.Publisher = strings.TrimSpace(s.Publisher)
		if s.Name == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b api.Software) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		if c := strings.Compare(a.Version, b.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Publisher, b.Publisher)
	})
	if len(out) > MaxSoftware {
		out = out[:MaxSoftware]
	}
	return out
}

// Interfaces lists interfaces that are up and have at least one address
// that is not link-local. That drops loopback, tunnels with no address and
// container veth pairs.
func Interfaces() []api.NetworkInterface {
	out := []api.NetworkInterface{}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		ips := []string{}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLoopback() {
				continue
			}
			ips = append(ips, ipn.String())
		}
		if len(ips) == 0 {
			continue
		}
		slices.Sort(ips)
		out = append(out, api.NetworkInterface{Name: i.Name, MAC: i.HardwareAddr.String(), IPs: ips})
	}
	slices.SortFunc(out, func(a, b api.NetworkInterface) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func uniqueSorted(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func clean(s string) string {
	return strings.TrimSpace(strings.Trim(s, "\x00"))
}
