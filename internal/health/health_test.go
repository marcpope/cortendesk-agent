package health

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
)

func TestSafeServiceName(t *testing.T) {
	ok := []string{
		"Spooler", "MSSQL$SQLEXPRESS", "com.openssh.sshd", "nginx", "ssh.service",
		"getty@tty1", "SQL Server (MSSQLSERVER)", "my_service-2", "Dienst für Drucker",
		strings.Repeat("a", 128),
		// Only allowed characters. Harmless, since names never pass
		// through a shell.
		"$(reboot)",
	}
	bad := []string{
		"", " ", "-H", "--help", strings.Repeat("a", 129),
		`a"b`, "a'b", "a`b", "a;b", "a|b", "a&b", "a<b", "a>b",
		"a\nb", "a\rb", "a\tb", "a\x00b", "a\x1bb",
		"a/b", `a\b`, "a*b", "a{b}", "a,b", "a#b", "a%b", "a!b",
	}
	for _, s := range ok {
		if !SafeServiceName(s) {
			t.Errorf("%q should be allowed", s)
		}
	}
	for _, s := range bad {
		if SafeServiceName(s) {
			t.Errorf("%q should be dropped", s)
		}
	}
}

func TestSanitizeServices(t *testing.T) {
	got := SanitizeServices([]string{"b", "a;rm -rf /", "a", "b", "", "Spooler"})
	if want := []string{"Spooler", "a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

type counter struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *counter) inc(k string) {
	c.mu.Lock()
	c.n[k]++
	c.mu.Unlock()
}

func (c *counter) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

func fakeChecks(calls *counter) []Check {
	return []Check{
		{Key: "firewall", Timeout: time.Second, Run: func(ctx context.Context, _ []string, out *api.Health) error {
			calls.inc("firewall")
			out.Firewall = ptr(true)
			return nil
		}},
		{Key: "updates", Timeout: 50 * time.Millisecond, Run: func(ctx context.Context, _ []string, out *api.Health) error {
			calls.inc("updates")
			<-ctx.Done() // hangs past its timeout
			out.Updates = &api.Updates{Missing: ptr(1)}
			return ctx.Err()
		}},
		{Key: "smart", Timeout: time.Second, Run: func(ctx context.Context, _ []string, out *api.Health) error {
			calls.inc("smart")
			out.Smart = []api.Smart{{Device: "x"}}
			return errors.New("broken")
		}},
		{Key: "services", Timeout: time.Second, Run: func(ctx context.Context, watch []string, out *api.Health) error {
			calls.inc("services")
			for _, w := range watch {
				out.Services = append(out.Services, api.Service{Name: w, State: api.ServiceMissing})
			}
			return nil
		}},
	}
}

func TestManager(t *testing.T) {
	calls := &counter{n: map[string]int{}}
	m := &Manager{Checks: fakeChecks(calls), Now: func() time.Time { return time.Unix(1762527600, 0) }}
	if m.Get() != nil {
		t.Fatal("health before any check should be nil")
	}
	<-m.Refresh(context.Background(), "")
	h := m.Get()
	if h == nil || h.Firewall == nil || !*h.Firewall {
		t.Fatalf("firewall missing: %+v", h)
	}
	if h.Updates != nil {
		t.Fatal("timed out check must be left out")
	}
	if h.Smart != nil {
		t.Fatal("failed check must be left out")
	}
	if h.Services != nil {
		t.Fatal("no watch list, no services")
	}
	if h.CollectedAt != 1762527600 {
		t.Fatalf("collected_at %d", h.CollectedAt)
	}
	b, _ := json.Marshal(h)
	if got := string(b); got != `{"firewall":true,"collected_at":1762527600}` {
		t.Fatalf("json %s", got)
	}

	if !m.SetWatch([]string{"Spooler", "bad|name"}) {
		t.Fatal("watch change not reported")
	}
	if m.SetWatch([]string{"Spooler"}) {
		t.Fatal("same list after sanitizing is not a change")
	}
	<-m.Refresh(context.Background(), "services")
	h = m.Get()
	if len(h.Services) != 1 || h.Services[0].Name != "Spooler" || h.Services[0].State != "missing" {
		t.Fatalf("services %+v", h.Services)
	}
	if calls.get("firewall") != 1 {
		t.Fatal("services-only refresh ran other checks")
	}
	m.SetWatch(nil)
	if m.Get().Services != nil {
		t.Fatal("cleared watch list keeps services")
	}
}

func TestParsers(t *testing.T) {
	netsh := "    Name                   : Wi-Fi\r\n    BSSID                  : aa:bb:cc:dd:ee:ff\r\n    SSID                   : Office: 5G\r\n"
	if got := ParseNetshSSID(netsh); got != "Office: 5G" {
		t.Errorf("netsh %q", got)
	}
	if got := ParseIPConfigSSID("  SSID : <redacted>\n"); got != "" {
		t.Errorf("redacted %q", got)
	}
	if got := ParseIPConfigSSID("  SSID : Home\n"); got != "Home" {
		t.Errorf("ipconfig %q", got)
	}
	if got := ParseWifiDevice("Hardware Port: Ethernet\nDevice: en0\n\nHardware Port: Wi-Fi\nDevice: en1\n"); got != "en1" {
		t.Errorf("wifi device %q", got)
	}
	if v := ParseSocketFilterFW("Firewall is disabled. (State = 0)"); v == nil || *v {
		t.Error("socketfilterfw disabled")
	}
	if v := ParseSocketFilterFW("Firewall is enabled. (State = 1)"); v == nil || !*v {
		t.Error("socketfilterfw enabled")
	}
	if e := ParseFdesetup("FileVault is On.\n"); e == nil || !e.Encrypted || e.Method != "FileVault" {
		t.Errorf("fdesetup on %+v", e)
	}
	if e := ParseFdesetup("FileVault is Off.\n"); e == nil || e.Encrypted {
		t.Errorf("fdesetup off %+v", e)
	}
	if d := ParseDiskutilList("/dev/disk0 (internal, physical):\n   #: TYPE\n/dev/disk3 (synthesized):\n/dev/disk6 (external, physical):\n"); !slices.Equal(d, []string{"disk0", "disk6"}) {
		t.Errorf("diskutil list %v", d)
	}
	if s := ParseDiskutilSmart("disk0", "   Device / Media Name:       APPLE SSD\n   SMART Status:              Verified\n"); s == nil || !s.Healthy || s.Model != "APPLE SSD" {
		t.Errorf("diskutil smart %+v", s)
	}
	if s := ParseDiskutilSmart("disk6", "   SMART Status:              Not Supported\n"); s != nil {
		t.Errorf("not supported should be nil: %+v", s)
	}
	lc := "system/com.openssh.sshd = {\n\tactive count = 0\n\tstate = not running\n\tendpoints = {\n\t\tstate = active\n\t}\n}\n"
	if got := ParseLaunchctlState(lc); got != "stopped" {
		t.Errorf("launchctl %q", got)
	}
	if got := ParseLaunchctlState("x = {\n\tstate = running\n}"); got != "running" {
		t.Errorf("launchctl running %q", got)
	}
	if st, d := ParseSystemctlShow("LoadState=loaded\nActiveState=active\nDescription=OpenSSH server\n"); st != "running" || d != "OpenSSH server" {
		t.Errorf("systemctl %q %q", st, d)
	}
	if st, _ := ParseSystemctlShow("LoadState=not-found\nActiveState=inactive\n"); st != "missing" {
		t.Errorf("systemctl missing %q", st)
	}
	if st, _ := ParseSystemctlShow("LoadState=loaded\nActiveState=failed\n"); st != "stopped" {
		t.Errorf("systemctl failed %q", st)
	}
	if v := ParseUFW("Status: active\n\nTo Action From\n"); v == nil || !*v {
		t.Error("ufw active")
	}
	if v := ParseFirewalld("not running\n"); v == nil || *v {
		t.Error("firewalld not running")
	}
	if n := ParseAptSimulate("Reading...\nInst libc6 [2.39] (2.39-1 Ubuntu)\nInst bash [5.2] (5.2-2)\nConf libc6\n"); n != 2 {
		t.Errorf("apt %d", n)
	}
	if n := ParseDnfCheckUpdate("\nkernel.x86_64   5.14.0-503   baseos\nbash.x86_64   5.1.8-10   baseos\nObsoleting Packages\nfoo.x86_64  1-1  appstream\n"); n != 2 {
		t.Errorf("dnf %d", n)
	}
	scan := ParseSmartctlScan("/dev/sda -d sat # /dev/sda [SAT], ATA device\n/dev/nvme0 -d nvme # /dev/nvme0, NVMe device\n")
	if len(scan) != 2 || scan[0] != [2]string{"/dev/sda", "sat"} {
		t.Errorf("smartctl scan %v", scan)
	}
	if s := ParseSmartctl("/dev/sda", "Device Model:     Samsung SSD 870\nSMART overall-health self-assessment test result: PASSED\n"); s == nil || !s.Healthy || s.Model != "Samsung SSD 870" {
		t.Errorf("smartctl %+v", s)
	}
	if s := ParseSmartctl("/dev/sdb", "Product:  ST4000\nSMART Health Status: FAILURE PREDICTION THRESHOLD EXCEEDED\n"); s == nil || s.Healthy {
		t.Errorf("smartctl scsi %+v", s)
	}
}
