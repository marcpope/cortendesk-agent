// Package agent is the report loop: enroll if needed, report on the
// console's interval, send inventory when asked or once a day.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/client"
	"github.com/marcpope/cortendesk-agent/internal/collect"
	"github.com/marcpope/cortendesk-agent/internal/commands"
	"github.com/marcpope/cortendesk-agent/internal/config"
	"github.com/marcpope/cortendesk-agent/internal/health"
	"github.com/marcpope/cortendesk-agent/internal/inventory"
	"github.com/marcpope/cortendesk-agent/internal/version"
)

const (
	defaultInterval = 60 * time.Second
	minInterval     = 30 * time.Second
	maxInterval     = time.Hour
	disabledWait    = 900 * time.Second
	revokedWait     = time.Hour
	backoffStart    = 30 * time.Second
	backoffMax      = 10 * time.Minute
	inventoryMaxAge = 24 * time.Hour
)

// Source produces report bodies. *collect.Collector is the real one.
type Source interface {
	Report(ctx context.Context) api.Report
	Identity(ctx context.Context) (machineUUID, rustdeskID string)
}

type Runner struct {
	Paths  config.Paths
	Config *config.Config
	Log    *slog.Logger

	// Optional; defaults are the real collectors and clock.
	Source    Source
	Inventory func(context.Context) api.Inventory
	Now       func() time.Time
	// Jitter returns a random duration in [0, max].
	Jitter func(max time.Duration) time.Duration
	// Health caches the slow checks. Run creates and starts one when nil;
	// Step alone leaves it alone, so tests can drive it.
	Health *health.Manager
	// Commands runs what the console queues; nil runs nothing and
	// advertises no capabilities. Run starts its worker; Step leaves it
	// to the caller.
	Commands *commands.Runner
	// OnReached is called after every report the console answered (200 or
	// 403). The update guard uses it to confirm a new version.
	OnReached func()

	client   *client.Client
	state    config.State
	interval time.Duration
	failures int
}

// Prepare fills defaults, builds the HTTP client and loads state. Run calls
// it if needed; calling it first surfaces config errors early.
func (r *Runner) Prepare() error {
	if r.Source == nil {
		r.Source = collect.New()
	}
	if r.Inventory == nil {
		r.Inventory = inventory.Collect
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Jitter == nil {
		r.Jitter = func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(max) + 1))
		}
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
	c, err := client.New(r.Config.Server, r.Config.CAFile)
	if err != nil {
		return err
	}
	c.AgentID, c.Secret = r.Config.AgentID, r.Config.Secret
	r.client = c
	if r.Commands != nil && r.Commands.Post == nil {
		r.Commands.Post = r.client.CommandResult
	}
	r.state = config.LoadState(r.Paths.State)
	r.interval = defaultInterval
	r.setInterval(r.state.Interval)
	return nil
}

// Run loops until ctx is cancelled. It only returns an error when the config
// cannot work at all (bad server URL, unreadable CA file).
func (r *Runner) Run(ctx context.Context) error {
	if r.client == nil {
		if err := r.Prepare(); err != nil {
			return err
		}
	}
	r.Log.Info("agent started", "version", version.Version, "server", r.Config.Server, "agent_id", r.Config.AgentID)
	if r.Health == nil {
		r.Health = health.New(r.Log)
	}
	go r.Health.Run(ctx)
	if r.Commands != nil {
		go r.Commands.Run(ctx)
	}
	for {
		d := r.Step(ctx)
		select {
		case <-ctx.Done():
			r.Log.Info("agent stopping")
			return nil
		case <-time.After(d):
		}
	}
}

// Step does one enroll or report round and returns how long to wait before
// the next one.
func (r *Runner) Step(ctx context.Context) time.Duration {
	if r.client == nil {
		if err := r.Prepare(); err != nil {
			r.Log.Error("bad config", "err", err)
			return revokedWait
		}
	}
	if !r.Config.Enrolled() {
		return r.enroll(ctx)
	}
	return r.report(ctx)
}

func (r *Runner) enroll(ctx context.Context) time.Duration {
	resp, err := Enroll(ctx, r.client, r.Source, r.Config.Token)
	switch status := client.StatusOf(err); {
	case err == nil:
		r.Config.AgentID, r.Config.Secret, r.Config.Token = resp.AgentID, resp.Secret, ""
		if err := config.Save(r.Paths.Config, r.Config); err != nil {
			// Keep running with the new identity; a restart would re-enroll.
			r.Log.Error("enrolled but could not save config", "err", err)
		}
		r.client.AgentID, r.client.Secret = resp.AgentID, resp.Secret
		r.state = config.State{}
		r.failures = 0
		r.setInterval(resp.Interval)
		r.Log.Info("enrolled", "agent_id", resp.AgentID)
		return 0
	case status == http.StatusUnauthorized:
		r.Log.Error("enrollment token rejected, retrying in an hour", "err", err)
		r.failures = 0
		return revokedWait
	default:
		d := r.backoff()
		r.Log.Warn("enroll failed", "err", err, "retry_in", d.Round(time.Second))
		return d
	}
}

func (r *Runner) report(ctx context.Context) time.Duration {
	rep := r.Source.Report(ctx)
	rep.InventoryHash = r.state.InventoryHash
	rep.Health = r.Health.Get()
	rep.Capabilities = []string{}
	if r.Commands != nil {
		rep.Capabilities = r.Commands.Capabilities()
	}
	resp, err := r.client.Report(ctx, rep)
	now := r.Now()
	r.state.LastReportAt = now
	defer r.saveState()

	switch status := client.StatusOf(err); {
	case err == nil:
		r.failures = 0
		r.setInterval(resp.Interval)
		r.state.LastResult, r.state.LastError, r.state.LastSuccessAt = "ok", "", now
		if r.Health.SetWatch(resp.WatchServices) {
			r.Log.Info("watched services changed", "services", r.Health.Watch())
		}
		r.reached()
		if r.Commands != nil {
			r.Commands.Flush(ctx)
			r.Commands.Offer(ctx, resp.Commands)
		}
		if resp.Inventory || r.inventoryDue(now) {
			r.sendInventory(ctx)
		}
		return r.withJitter(r.interval)
	case status == http.StatusUnauthorized:
		// Agent deleted in the console. Check back hourly in case it is
		// re-created.
		r.failures = 0
		r.state.LastResult, r.state.LastError = "revoked", err.Error()
		r.Log.Warn("agent revoked by the console, retrying in an hour")
		return revokedWait
	case status == http.StatusForbidden:
		r.failures = 0
		r.reached()
		r.state.LastResult, r.state.LastError = "disabled", err.Error()
		d := disabledWait
		if resp.Interval > 0 {
			d = clampInterval(time.Duration(resp.Interval) * time.Second)
		}
		r.Log.Info("agent disabled in the console", "retry_in", d)
		return r.withJitter(d)
	default:
		r.state.LastResult, r.state.LastError = "error", err.Error()
		d := r.backoff()
		r.Log.Warn("report failed", "err", err, "retry_in", d.Round(time.Second))
		return d
	}
}

func (r *Runner) reached() {
	if r.OnReached != nil {
		r.OnReached()
	}
}

// Trial sends one report and saves nothing. A downloaded update runs this
// before it is swapped in, to prove it can start, read the config and get
// a report accepted.
func (r *Runner) Trial(ctx context.Context) error {
	if r.client == nil {
		if err := r.Prepare(); err != nil {
			return err
		}
	}
	if !r.Config.Enrolled() {
		return errors.New("not enrolled")
	}
	rep := r.Source.Report(ctx)
	rep.InventoryHash = r.state.InventoryHash
	rep.Capabilities = []string{}
	rep.Trial = true
	_, err := r.client.Report(ctx, rep)
	return err
}

func (r *Runner) inventoryDue(now time.Time) bool {
	return r.state.InventoryHash == "" || now.Sub(r.state.InventoryAt) >= inventoryMaxAge
}

func (r *Runner) sendInventory(ctx context.Context) {
	inv := r.Inventory(ctx)
	if err := r.client.Inventory(ctx, inv); err != nil {
		r.Log.Warn("inventory failed", "err", err)
		return
	}
	r.state.InventoryHash, r.state.InventoryAt = inv.Hash, r.Now()
	r.Log.Info("inventory sent", "hash", inv.Hash, "software", len(inv.Software))
}

func (r *Runner) saveState() {
	r.state.Interval = int(r.interval / time.Second)
	if r.Paths.State == "" {
		return
	}
	if err := config.SaveState(r.Paths.State, r.state); err != nil {
		r.Log.Warn("could not save state", "err", err)
	}
}

func (r *Runner) setInterval(sec int) {
	if sec > 0 {
		r.interval = clampInterval(time.Duration(sec) * time.Second)
	}
}

func clampInterval(d time.Duration) time.Duration {
	return min(max(d, minInterval), maxInterval)
}

// backoff gives 30s, 60s, 120s ... capped at 10 minutes, plus jitter so a
// fleet does not come back in lockstep after a console outage.
func (r *Runner) backoff() time.Duration {
	d := backoffStart
	for i := 0; i < r.failures && d < backoffMax; i++ {
		d *= 2
	}
	r.failures++
	return r.withJitter(min(d, backoffMax))
}

func (r *Runner) withJitter(d time.Duration) time.Duration {
	return d + r.Jitter(d/10)
}

// Enroll sends an enroll request built from this machine's identity.
func Enroll(ctx context.Context, c *client.Client, src Source, token string) (api.EnrollResponse, error) {
	uuid, rdID := src.Identity(ctx)
	osInfo := inventory.OS(ctx)
	host, _ := os.Hostname()
	return c.Enroll(ctx, api.EnrollRequest{
		Token:        token,
		MachineUUID:  uuid,
		RustDeskID:   rdID,
		Hostname:     host,
		OS:           runtime.GOOS,
		OSVersion:    inventory.OSVersion(osInfo),
		Arch:         runtime.GOARCH,
		AgentVersion: version.Version,
	})
}

// State returns the current run state, for tests.
func (r *Runner) State() config.State { return r.state }
