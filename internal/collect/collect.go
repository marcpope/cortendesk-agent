// Package collect builds the health part of a report: CPU, memory, disks,
// network, uptime, users, reboot state and RustDesk state.
package collect

import (
	"context"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/identity"
	"github.com/marcpope/cortendesk-agent/internal/version"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// rustdeskTTL limits how often the agent launches `rustdesk --get-id`.
const rustdeskTTL = 5 * time.Minute

// Collector keeps the state needed between reports: the previous CPU times
// (so CPU% is the average over the interval) and cached RustDesk facts.
type Collector struct {
	mu sync.Mutex

	prevCPU *cpu.TimesStat

	machineUUID string

	rdAt        time.Time
	rdID        string
	rdInstalled bool
	rdVersion   string
}

func New() *Collector { return &Collector{} }

// Identity returns machine_uuid and rustdesk_id, both possibly empty.
func (c *Collector) Identity(ctx context.Context) (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identityLocked(ctx)
}

func (c *Collector) identityLocked(ctx context.Context) (string, string) {
	if c.machineUUID == "" {
		c.machineUUID, _ = identity.MachineUUID(ctx)
	}
	if c.rdAt.IsZero() || time.Since(c.rdAt) > rustdeskTTL {
		rd := identity.RustDesk(ctx)
		c.rdInstalled, c.rdVersion = rd.Installed, rd.Version
		c.rdID = identity.RustDeskID(ctx)
		c.rdAt = time.Now()
	}
	return c.machineUUID, c.rdID
}

// Report gathers everything except inventory_hash, which the caller owns.
// A failing collector leaves its fields zero; it never fails the report.
func (c *Collector) Report(ctx context.Context) api.Report {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := api.Report{AgentVersion: version.Version}
	r.MachineUUID, r.RustDeskID = c.identityLocked(ctx)
	r.Hostname, _ = os.Hostname()
	r.Uptime, _ = host.UptimeWithContext(ctx)

	r.CPU = c.cpu(ctx)
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		r.Memory.Total, r.Memory.Used = vm.Total, vm.Used
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		r.Memory.SwapTotal, r.Memory.SwapUsed = sw.Total, sw.Used
	}
	r.Disks = Disks(ctx)
	r.Network = NetworkTotals(ctx)
	r.RustDesk = api.RustDesk{
		Installed: c.rdInstalled,
		Running:   identity.RustDeskRunning(),
		Version:   c.rdVersion,
	}
	r.Users = users(ctx)
	r.RebootPending = rebootPending()
	return r
}

func (c *Collector) cpu(ctx context.Context) api.CPU {
	var out api.CPU
	out.Cores, _ = cpu.CountsWithContext(ctx, true)
	if hasLoadAvg {
		if l, err := load.AvgWithContext(ctx); err == nil {
			out.Load = []float64{round2(l.Load1), round2(l.Load5), round2(l.Load15)}
		}
	}

	now, err := cpuTimes(ctx)
	if err != nil {
		return out
	}
	prev := c.prevCPU
	if prev == nil || total(now)-total(prev) <= 0 {
		// First report: take a one second sample.
		select {
		case <-ctx.Done():
			return out
		case <-time.After(time.Second):
		}
		prev = now
		if now, err = cpuTimes(ctx); err != nil {
			return out
		}
	}
	c.prevCPU = now
	out.Percent = round2(busyPercent(prev, now))
	return out
}

func cpuTimes(ctx context.Context) (*cpu.TimesStat, error) {
	t, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(t) == 0 {
		return nil, err
	}
	return &t[0], nil
}

// Guest time is already counted in User on Linux, so it is left out.
func total(t *cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func busyPercent(a, b *cpu.TimesStat) float64 {
	dt := total(b) - total(a)
	if dt <= 0 {
		return 0
	}
	idle := (b.Idle + b.Iowait) - (a.Idle + a.Iowait)
	p := (dt - idle) / dt * 100
	return min(max(p, 0), 100)
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// Disks lists real filesystems with their usage.
func Disks(ctx context.Context) []api.Disk {
	out := []api.Disk{}
	parts, _ := disk.PartitionsWithContext(ctx, false) // may return partial results with an error
	for _, p := range FilterPartitions(parts, goos) {
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, api.Disk{Mount: p.Mountpoint, FS: p.Fstype, Total: u.Total, Used: u.Used})
	}
	return out
}

// NetworkTotals sums bytes and packets since boot over every non-loopback
// interface.
func NetworkTotals(ctx context.Context) api.NetTotals {
	var t api.NetTotals
	counters, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return t
	}
	loop := loopbackNames()
	for _, c := range counters {
		if IsLoopbackName(c.Name, loop) {
			continue
		}
		t.RxBytes += c.BytesRecv
		t.TxBytes += c.BytesSent
		t.RxPackets += c.PacketsRecv
		t.TxPackets += c.PacketsSent
	}
	return t
}

func loopbackNames() map[string]bool {
	m := map[string]bool{}
	ifs, err := net.Interfaces()
	if err != nil {
		return m
	}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			m[i.Name] = true
		}
	}
	return m
}

// IsLoopbackName also catches the usual names in case the flag lookup and
// the counters disagree on naming.
func IsLoopbackName(name string, known map[string]bool) bool {
	if known[name] {
		return true
	}
	n := strings.ToLower(name)
	return n == "lo" || n == "lo0" || strings.Contains(n, "loopback")
}

func uniqueSorted(in []string) []string {
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
