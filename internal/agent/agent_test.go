package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/agent"
	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/commands"
	"github.com/marcpope/cortendesk-agent/internal/config"
	"github.com/marcpope/cortendesk-agent/internal/fakeconsole"
	"github.com/marcpope/cortendesk-agent/internal/health"
)

func servicesSupported() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

const token = "cde_testtokentesttokentesttokentesttoken12"

func keys(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func wantKeys(t *testing.T, what string, m map[string]any, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := keys(m); !slices.Equal(got, want) {
		t.Errorf("%s keys:\n got  %v\n want %v", what, got, want)
	}
}

func obj(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", what, v)
	}
	return m
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func isNumber(v any) bool { _, ok := v.(float64); return ok }
func isString(v any) bool { _, ok := v.(string); return ok }

type rig struct {
	t       *testing.T
	console *fakeconsole.Console
	cfgPath string
	runner  *agent.Runner
	now     time.Time
}

func newRig(t *testing.T) *rig {
	c := fakeconsole.New(token, 45, true)
	t.Cleanup(c.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), c.CAPEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	// Relative ca_file resolves next to the config.
	raw := `{"server": "` + c.URL() + `/", "token": "` + token + `", "ca_file": "ca.pem"}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, console: c, cfgPath: cfgPath, now: time.Now()}
	r.runner = r.newRunner()
	return r
}

func (r *rig) newRunner() *agent.Runner {
	return r.prepare(&agent.Runner{})
}

// prepare fills run with the rig's config, clock and logger, keeping any
// Commands or OnReached already set.
func (r *rig) prepare(run *agent.Runner) *agent.Runner {
	cfg, err := config.Load(r.cfgPath)
	if err != nil {
		r.t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run.Paths = config.ForConfig(r.cfgPath)
	run.Config = cfg
	run.Log = log
	run.Health = health.New(log)
	run.Now = func() time.Time { return r.now }
	run.Jitter = func(time.Duration) time.Duration { return 0 }
	if err := run.Prepare(); err != nil {
		r.t.Fatal(err)
	}
	return run
}

func (r *rig) step(want time.Duration) {
	r.t.Helper()
	if got := r.runner.Step(context.Background()); got != want {
		r.t.Fatalf("step waited %v, want %v (state %+v)", got, want, r.runner.State())
	}
}

func TestEndToEnd(t *testing.T) {
	r := newRig(t)
	c := r.console

	// 1. Enroll.
	r.step(0)
	enrolls := c.Requests("/agent/v1/enroll")
	if len(enrolls) != 1 || enrolls[0].Status != 201 {
		t.Fatalf("enroll requests: %+v", enrolls)
	}
	e := decode(t, enrolls[0].Body)
	wantKeys(t, "enroll", e, "token", "machine_uuid", "rustdesk_id", "hostname", "os", "os_version", "arch", "agent_version")
	if e["token"] != token || e["os"] != runtime.GOOS || e["arch"] != runtime.GOARCH || e["machine_uuid"] == "" {
		t.Errorf("enroll body: %v", e)
	}
	saved, err := config.Load(r.cfgPath)
	if err != nil || !saved.Enrolled() || saved.Token != "" {
		t.Fatalf("config after enroll: %+v %v", saved, err)
	}

	// 2. First report: console has no inventory, asks for one, agent sends
	// it, console checks the hash.
	r.step(45 * time.Second)
	reports := c.Requests("/agent/v1/report")
	if len(reports) != 1 || reports[0].Status != 200 {
		t.Fatalf("reports: %+v", reports)
	}
	checkReportShape(t, decode(t, reports[0].Body))
	invs := c.Requests("/agent/v1/inventory")
	if len(invs) != 1 || invs[0].Status != 200 {
		t.Fatalf("inventory requests: %+v", invs)
	}
	inv := decode(t, invs[0].Body)
	checkInventoryShape(t, inv)
	hash := inv["hash"].(string)

	// 3. Second report carries the hash; no new inventory.
	r.step(45 * time.Second)
	rep := decode(t, c.Requests("/agent/v1/report")[1].Body)
	if rep["inventory_hash"] != hash {
		t.Fatalf("inventory_hash %v, want %s", rep["inventory_hash"], hash)
	}
	if n := len(c.Requests("/agent/v1/inventory")); n != 1 {
		t.Fatalf("unexpected inventory, %d sent", n)
	}

	// 4. Console asks again (hash it stores is gone).
	c.ForgetInventory()
	r.step(45 * time.Second)
	if n := len(c.Requests("/agent/v1/inventory")); n != 2 {
		t.Fatalf("console asked for inventory, %d sent", n)
	}

	// 5. 24h later the agent resends without being asked.
	r.now = r.now.Add(25 * time.Hour)
	r.step(45 * time.Second)
	if n := len(c.Requests("/agent/v1/inventory")); n != 3 {
		t.Fatalf("daily inventory not sent, %d sent", n)
	}

	// 6. Health: nothing until the first refresh; then the cached copy,
	// with watched services from the console (unsafe names dropped).
	if _, ok := rep["health"]; ok {
		t.Fatal("health sent before any check ran")
	}
	c.SetWatch([]string{"cortendesk-test-missing", "bad;name"})
	r.step(45 * time.Second) // response carries the watch list
	<-r.runner.Health.Refresh(context.Background(), "")
	r.step(45 * time.Second)
	all := c.Requests("/agent/v1/report")
	h := obj(t, decode(t, all[len(all)-1].Body)["health"], "health")
	allowed := []string{"antivirus", "firewall", "encryption", "updates", "smart", "services", "event_errors_24h", "wifi_ssid", "collected_at"}
	for k := range h {
		if !slices.Contains(allowed, k) {
			t.Errorf("unknown health key %q", k)
		}
	}
	if !isNumber(h["collected_at"]) {
		t.Errorf("collected_at: %v", h["collected_at"])
	}
	if servicesSupported() {
		svcs, _ := h["services"].([]any)
		if len(svcs) != 1 {
			t.Fatalf("services: %v", h["services"])
		}
		s := obj(t, svcs[0], "service")
		if s["name"] != "cortendesk-test-missing" || s["state"] != "missing" {
			t.Errorf("service: %v", s)
		}
	}
	t.Logf("health on this machine: %v", h)

	// 7. Interval change applies on the next report.
	c.SetInterval(120)
	r.step(120 * time.Second)

	// 8. Disabled: wait the 900 the console returns.
	c.SetMode(fakeconsole.ModeDisabled)
	r.step(900 * time.Second)
	if st := r.runner.State(); st.LastResult != "disabled" {
		t.Fatalf("state %+v", st)
	}

	// 9. Revoked: retry hourly.
	c.SetMode(fakeconsole.ModeRevoked)
	r.step(time.Hour)
	if st := r.runner.State(); st.LastResult != "revoked" {
		t.Fatalf("state %+v", st)
	}

	// 10. Network errors back off 30s, 60s, 120s; success resets it.
	c.SetMode(fakeconsole.ModeDown)
	r.step(30 * time.Second)
	r.step(60 * time.Second)
	r.step(120 * time.Second)
	c.SetMode(fakeconsole.ModeNormal)
	r.step(120 * time.Second)
	c.SetMode(fakeconsole.ModeDown)
	r.step(30 * time.Second)
	c.SetMode(fakeconsole.ModeNormal)

	// 11. State file is what `status` reads.
	st := config.LoadState(filepath.Join(filepath.Dir(r.cfgPath), "state.json"))
	if st.LastResult != "error" || st.InventoryHash == "" || st.Interval != 120 {
		t.Fatalf("state file: %+v", st)
	}
}

func TestCommandsRunAndReport(t *testing.T) {
	r := newRig(t)
	ran := 0
	confirmed := 0
	r.runner.OnReached = func() { confirmed++ }
	r.runner.Commands = &commands.Runner{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store: commands.OpenStore(filepath.Join(filepath.Dir(r.cfgPath), "commands.json"), slog.New(slog.NewTextHandler(io.Discard, nil))),
		Handlers: map[string]commands.Spec{
			api.CmdReboot: {Timeout: time.Second, Run: func(_ context.Context, p json.RawMessage, out *commands.Output) error {
				ran++
				out.Printf("params %s", p)
				return nil
			}},
		},
	}
	r.runner = r.prepare(r.runner)
	c := r.console
	r.step(0)

	c.Queue(api.Command{ID: "01CMD1", Type: api.CmdReboot, Params: json.RawMessage(`{"delay":60}`)})
	c.Queue(api.Command{ID: "01CMD2", Type: "format.disk"})
	r.step(45 * time.Second)
	r.runner.Commands.Drain(context.Background())

	rep := decode(t, c.Requests("/agent/v1/report")[0].Body)
	if caps, _ := rep["capabilities"].([]any); len(caps) != 1 || caps[0] != api.CmdReboot {
		t.Fatalf("capabilities %v", rep["capabilities"])
	}
	res := c.Results("01CMD1")
	if ran != 1 || len(res) != 2 || res[0].Status != "running" || res[1].Status != "succeeded" || res[1].Output != "params {\"delay\":60}\n" {
		t.Fatalf("ran %d results %+v", ran, res)
	}
	if res := c.Results("01CMD2"); len(res) != 1 || res[0].Status != "failed" {
		t.Fatalf("unknown type: %+v", res)
	}
	if confirmed != 1 {
		t.Fatalf("OnReached called %d times", confirmed)
	}

	// Redelivered: not run again. Console down when the result is due:
	// kept and sent after the next good report.
	c.Queue(api.Command{ID: "01CMD1", Type: api.CmdReboot})
	c.Queue(api.Command{ID: "01CMD3", Type: api.CmdReboot})
	r.step(45 * time.Second)
	c.SetMode(fakeconsole.ModeDown)
	r.runner.Commands.Drain(context.Background())
	if ran != 2 || len(c.Results("01CMD3")) != 0 {
		t.Fatalf("ran %d, results %+v", ran, c.Results("01CMD3"))
	}
	c.SetMode(fakeconsole.ModeNormal)
	r.step(45 * time.Second)
	if res := c.Results("01CMD3"); len(res) != 1 || res[0].Status != "succeeded" {
		t.Fatalf("held result: %+v", res)
	}
}

func TestTrialReportsOnceAndSavesNothing(t *testing.T) {
	r := newRig(t)
	r.step(0)
	before, _ := os.ReadFile(filepath.Join(filepath.Dir(r.cfgPath), "state.json"))
	r.console.Queue(api.Command{ID: "01KEEP", Type: api.CmdReboot})

	cfg, _ := config.Load(r.cfgPath)
	p := config.ForConfig(r.cfgPath)
	p.State = ""
	trial := &agent.Runner{Paths: p, Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := trial.Trial(context.Background()); err != nil {
		t.Fatal(err)
	}
	reps := r.console.Requests("/agent/v1/report")
	if len(reps) != 1 || decode(t, reps[0].Body)["trial"] != true {
		t.Fatalf("trial report: %+v", reps)
	}
	after, _ := os.ReadFile(filepath.Join(filepath.Dir(r.cfgPath), "state.json"))
	if string(before) != string(after) {
		t.Fatal("trial changed state.json")
	}
	// The queued command waits for the real agent.
	if r.console.Queued() != 1 {
		t.Fatal("trial report was handed the queued command")
	}
	r.step(45 * time.Second)
	if r.console.Queued() != 0 {
		t.Fatal("real report did not get the queued command")
	}
}

func TestBackoffCapsAtTenMinutes(t *testing.T) {
	r := newRig(t)
	r.step(0)
	r.console.SetMode(fakeconsole.ModeDown)
	for _, want := range []time.Duration{30, 60, 120, 240, 480, 600, 600} {
		r.step(want * time.Second)
	}
}

func TestReenrollSameMachineKeepsID(t *testing.T) {
	r := newRig(t)
	r.step(0)
	first, _ := config.Load(r.cfgPath)

	raw := `{"server": "` + r.console.URL() + `", "token": "` + token + `", "ca_file": "ca.pem"}`
	if err := os.WriteFile(r.cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	r.runner = r.newRunner()
	r.step(0)
	second, _ := config.Load(r.cfgPath)
	if second.AgentID != first.AgentID || second.Secret == first.Secret {
		t.Fatalf("re-enroll: first %+v second %+v", first, second)
	}
	// The old secret no longer works, the new one does.
	r.step(45 * time.Second)
}

func TestBadTokenRetriesHourly(t *testing.T) {
	r := newRig(t)
	raw := `{"server": "` + r.console.URL() + `", "token": "cde_wrong", "ca_file": "ca.pem"}`
	if err := os.WriteFile(r.cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	r.runner = r.newRunner()
	r.step(time.Hour)
}

func TestUntrustedCertFails(t *testing.T) {
	r := newRig(t)
	raw := `{"server": "` + r.console.URL() + `", "token": "` + token + `"}`
	if err := os.WriteFile(r.cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	r.runner = r.newRunner()
	// No CA file: TLS fails, which is a network error.
	r.step(30 * time.Second)
	if n := len(r.console.Requests("")); n != 0 {
		t.Fatalf("request got through without trusting the cert: %d", n)
	}
}

func checkReportShape(t *testing.T, m map[string]any) {
	t.Helper()
	wantKeys(t, "report", m, "agent_version", "machine_uuid", "rustdesk_id", "hostname", "uptime", "cpu",
		"memory", "disks", "network", "rustdesk", "users", "reboot_pending", "inventory_hash", "capabilities")
	cpu := obj(t, m["cpu"], "cpu")
	if runtime.GOOS == "windows" {
		wantKeys(t, "cpu", cpu, "percent", "cores")
	} else {
		wantKeys(t, "cpu", cpu, "percent", "cores", "load")
		if l, _ := cpu["load"].([]any); len(l) != 3 {
			t.Errorf("load: %v", cpu["load"])
		}
	}
	if !isNumber(cpu["percent"]) || cpu["cores"].(float64) < 1 {
		t.Errorf("cpu: %v", cpu)
	}
	mem := obj(t, m["memory"], "memory")
	wantKeys(t, "memory", mem, "total", "used", "swap_total", "swap_used")
	if mem["total"].(float64) <= 0 || mem["used"].(float64) <= 0 {
		t.Errorf("memory: %v", mem)
	}
	disks, ok := m["disks"].([]any)
	if !ok || len(disks) == 0 {
		t.Fatalf("disks: %v", m["disks"])
	}
	for _, d := range disks {
		dm := obj(t, d, "disk")
		wantKeys(t, "disk", dm, "mount", "fs", "total", "used")
		if !isString(dm["mount"]) || dm["total"].(float64) <= 0 {
			t.Errorf("disk: %v", dm)
		}
	}
	wantKeys(t, "network", obj(t, m["network"], "network"), "rx_bytes", "rx_packets", "tx_bytes", "tx_packets")
	wantKeys(t, "rustdesk", obj(t, m["rustdesk"], "rustdesk"), "installed", "running", "version")
	if _, ok := m["users"].([]any); !ok {
		t.Errorf("users is %T, want array", m["users"])
	}
	if _, ok := m["reboot_pending"].(bool); !ok {
		t.Errorf("reboot_pending: %v", m["reboot_pending"])
	}
	if !isNumber(m["uptime"]) || m["uptime"].(float64) <= 0 {
		t.Errorf("uptime: %v", m["uptime"])
	}
}

func checkInventoryShape(t *testing.T, m map[string]any) {
	t.Helper()
	if _, ok := m["battery"]; ok {
		wantKeys(t, "battery", obj(t, m["battery"], "battery"), "design_capacity", "full_capacity", "cycle_count")
		delete(m, "battery")
	}
	wantKeys(t, "inventory", m, "hash", "os", "hardware", "network", "software", "local_admins")
	if _, ok := m["local_admins"].([]any); !ok {
		t.Errorf("local_admins is %T", m["local_admins"])
	}
	wantKeys(t, "os", obj(t, m["os"], "os"), "name", "version", "build", "kernel")
	hw := obj(t, m["hardware"], "hardware")
	wantKeys(t, "hardware", hw, "manufacturer", "model", "serial", "bios", "cpu", "cores", "threads", "memory", "drives")
	if _, ok := hw["drives"].([]any); !ok {
		t.Errorf("drives is %T", hw["drives"])
	}
	nets, ok := m["network"].([]any)
	if !ok {
		t.Fatalf("network is %T", m["network"])
	}
	for _, n := range nets {
		wantKeys(t, "interface", obj(t, n, "interface"), "name", "mac", "ips")
	}
	sw, ok := m["software"].([]any)
	if !ok {
		t.Fatalf("software is %T", m["software"])
	}
	for _, s := range sw {
		wantKeys(t, "software", obj(t, s, "software"), "name", "version", "publisher")
	}
}
