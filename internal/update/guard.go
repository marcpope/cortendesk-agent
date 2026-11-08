package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/commands"
)

const (
	statusPending    = "pending"
	statusRolledBack = "rolled_back"
	// MaxStarts is how many times a new binary may start without a good
	// report before it is rolled back.
	MaxStarts = 3
	// ConfirmWithin is how long a new binary has to get a report through.
	ConfirmWithin = 10 * time.Minute
)

// record is update.json.
type record struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	CommandID string    `json:"command_id,omitempty"`
	Status    string    `json:"status"`
	Starts    int       `json:"starts"`
	At        time.Time `json:"at"`
	Reason    string    `json:"reason,omitempty"`
}

func recordPath(dir string) string { return filepath.Join(dir, "update.json") }

func loadRecord(dir string) (record, bool) {
	var r record
	b, err := os.ReadFile(recordPath(dir))
	if err != nil || json.Unmarshal(b, &r) != nil {
		return r, false
	}
	return r, true
}

func saveRecord(dir string, r record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := recordPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, recordPath(dir))
}

// Guard runs at every start. While an update is unconfirmed it watches the
// new binary and puts the old one back if the new one can't report.
type Guard struct {
	Dir     string
	Binary  string
	Current string
	Store   *commands.Store
	Log     *slog.Logger
	// Restart ends the process so the service manager starts the binary
	// that is now in place.
	Restart func()

	mu        sync.Mutex
	pending   bool
	confirmed chan struct{}
}

// Start reads update.json. It returns true when it rolled back, in which
// case the caller must Restart right away.
func (g *Guard) Start() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.confirmed = make(chan struct{})
	rec, ok := loadRecord(g.Dir)
	if !ok {
		return false
	}
	switch {
	case rec.Status == statusPending && g.Current == rec.To:
		rec.Starts++
		if rec.Starts > MaxStarts {
			return g.rollback(rec, fmt.Sprintf("%s started %d times without getting a report through", rec.To, MaxStarts))
		}
		if err := saveRecord(g.Dir, rec); err != nil {
			g.Log.Warn("could not update update.json", "err", err)
		}
		g.pending = true
		g.Log.Info("new version on trial", "from", rec.From, "to", rec.To, "start", rec.Starts)
		return false
	case rec.Status == statusRolledBack && g.Current == rec.From:
		g.Log.Warn("update rolled back", "to", rec.To, "reason", rec.Reason)
		g.fail(rec, "rolled back to "+rec.From+": "+rec.Reason)
	case rec.Status == statusPending && g.Current == rec.From:
		// The swap didn't stick (restored by hand, or a crash mid-swap).
		g.fail(rec, rec.To+" never started; still running "+rec.From)
	default:
		g.Log.Info("ignoring a stale update.json", "from", rec.From, "to", rec.To, "running", g.Current)
	}
	g.clear()
	return false
}

// fail reports the update command as failed and refuses that version.
func (g *Guard) fail(rec record, reason string) {
	g.Store.MarkBad(rec.To)
	if rec.CommandID != "" {
		g.Store.AddPending(rec.CommandID, api.CommandResult{Status: api.StatusFailed, Error: reason})
	}
}

func (g *Guard) clear() {
	if err := os.Remove(recordPath(g.Dir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		g.Log.Warn("could not remove update.json", "err", err)
	}
}

// Confirm is called after every report the console answered. The first one
// after an update commits it.
func (g *Guard) Confirm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.pending {
		return
	}
	rec, ok := loadRecord(g.Dir)
	g.pending = false
	close(g.confirmed)
	if !ok {
		return
	}
	g.clear()
	g.Log.Info("update confirmed", "from", rec.From, "to", rec.To)
	if rec.CommandID != "" {
		g.Store.AddPending(rec.CommandID, api.CommandResult{
			Status: api.StatusSucceeded,
			Output: "Updated from " + rec.From + " to " + rec.To + ".",
		})
	}
}

// Watch rolls back when no report gets through within d of starting. It
// returns at once when no update is on trial.
func (g *Guard) Watch(ctx context.Context, d time.Duration) {
	g.mu.Lock()
	pending, confirmed := g.pending, g.confirmed
	g.mu.Unlock()
	if !pending {
		return
	}
	select {
	case <-ctx.Done():
	case <-confirmed:
	case <-time.After(d):
		g.mu.Lock()
		if !g.pending {
			g.mu.Unlock()
			return
		}
		rec, ok := loadRecord(g.Dir)
		rolled := ok && g.rollback(rec, fmt.Sprintf("%s got no report through in %s", rec.To, d))
		g.mu.Unlock()
		if rolled {
			g.Restart()
		}
	}
}

// rollback puts the previous binary back and marks update.json so the old
// binary reports the failure. Without a previous binary it keeps the new
// one running, which is better than nothing.
func (g *Guard) rollback(rec record, reason string) bool {
	prev, bad := sibling(g.Binary, "prev"), sibling(g.Binary, "bad")
	if _, err := os.Stat(prev); err != nil {
		g.Log.Error("cannot roll back: no previous binary", "reason", reason)
		g.pending = false
		g.clear()
		if rec.CommandID != "" {
			g.Store.AddPending(rec.CommandID, api.CommandResult{Status: api.StatusFailed, Error: reason + "; no previous binary to roll back to"})
		}
		return false
	}
	_ = os.Remove(bad)
	if err := os.Rename(g.Binary, bad); err != nil {
		g.Log.Error("rollback: could not move the new binary aside", "err", err)
		return false
	}
	if err := os.Rename(prev, g.Binary); err != nil {
		_ = os.Rename(bad, g.Binary)
		g.Log.Error("rollback: could not restore the previous binary", "err", err)
		return false
	}
	rec.Status, rec.Reason = statusRolledBack, reason
	if err := saveRecord(g.Dir, rec); err != nil {
		g.Log.Warn("could not update update.json", "err", err)
	}
	g.pending = false
	g.Log.Error("rolled back", "to", rec.From, "reason", reason)
	return true
}
