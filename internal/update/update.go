package update

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/commands"
	"github.com/marcpope/cortendesk-agent/internal/download"
	"github.com/marcpope/cortendesk-agent/internal/execx"
	"github.com/marcpope/cortendesk-agent/internal/semver"
)

const (
	maxBinary    = 200 << 20
	trialTimeout = 2 * time.Minute
	// ExitRestart is the exit code that asks the service manager to start
	// the agent again.
	ExitRestart = 3
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Updater runs the agent.update command.
type Updater struct {
	// Binary is the running executable.
	Binary string
	// Dir holds update.json (the config directory).
	Dir        string
	ConfigPath string
	Current    string
	Keys       []ed25519.PublicKey
	Store      *commands.Store
	Log        *slog.Logger
	// Trial runs `<bin> trial` and returns its output. Tests replace it.
	Trial func(ctx context.Context, bin, configPath string) (string, error)
	// Restart ends the process so the service manager starts the new
	// binary. It does not return in production.
	Restart func()
	// GOOS and GOARCH default to this build's.
	GOOS, GOARCH string
}

type params struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// Run is the command handler. On success it swaps the binary, records the
// update and restarts; the result is sent by whichever binary ends up
// running (see Guard).
func (u *Updater) Run(ctx context.Context, raw json.RawMessage, out *commands.Output) error {
	goos, goarch := u.GOOS, u.GOARCH
	if goos == "" {
		goos, goarch = Platform()
	}
	var p params
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return fmt.Errorf("bad params: %w", err)
	}
	p.SHA256 = strings.ToLower(p.SHA256)
	switch {
	case !semver.Valid(p.Version):
		return fmt.Errorf("bad version %q", p.Version)
	case semver.Compare(p.Version, u.Current) <= 0:
		return fmt.Errorf("refusing %s: this agent is already %s", p.Version, u.Current)
	case u.Store.IsBad(p.Version):
		return fmt.Errorf("refusing %s: it failed to start here in the last 24 hours", p.Version)
	case !sha256Re.MatchString(p.SHA256):
		return errors.New("bad sha256")
	case download.CheckURL(p.URL) != nil:
		return download.CheckURL(p.URL)
	case !Verify(u.Keys, p.Version, goos, goarch, p.SHA256, p.Signature):
		return fmt.Errorf("the signature for %s %s/%s is not valid", p.Version, goos, goarch)
	}

	next := sibling(u.Binary, "new")
	out.Printf("Downloading %s", p.URL)
	if err := download.File(ctx, p.URL, next, p.SHA256, maxBinary); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(next) // gone after a successful swap anyway
	if err := os.Chmod(next, 0o755); err != nil {
		return err
	}
	out.Printf("Signature and sha256 verified.")

	trial := u.Trial
	if trial == nil {
		trial = runTrial
	}
	tctx, cancel := context.WithTimeout(ctx, trialTimeout)
	got, err := trial(tctx, next, u.ConfigPath)
	cancel()
	if want := "trial ok " + p.Version; err != nil || !strings.Contains(got, want) {
		out.Printf("%s", strings.TrimSpace(got))
		if err == nil {
			err = errors.New("unexpected output")
		}
		return fmt.Errorf("trial run of %s failed, nothing changed: %w", p.Version, err)
	}
	out.Printf("Trial report from %s accepted by the console.", p.Version)

	prev := sibling(u.Binary, "prev")
	_ = os.Remove(prev)
	if err := os.Rename(u.Binary, prev); err != nil {
		return fmt.Errorf("keep the current binary: %w", err)
	}
	if err := os.Rename(next, u.Binary); err != nil {
		if rerr := os.Rename(prev, u.Binary); rerr != nil {
			u.Log.Error("could not put the current binary back", "err", rerr)
		}
		return fmt.Errorf("swap: %w", err)
	}
	rec := record{From: u.Current, To: p.Version, CommandID: commands.IDFrom(ctx), Status: statusPending, At: time.Now()}
	if err := saveRecord(u.Dir, rec); err != nil {
		// Without the record nobody can roll back, so undo the swap.
		_ = os.Rename(u.Binary, next)
		_ = os.Rename(prev, u.Binary)
		return fmt.Errorf("write update.json: %w", err)
	}
	u.Log.Info("agent binary replaced, restarting", "from", u.Current, "to", p.Version)
	u.Restart()
	return commands.ErrResultLater
}

// Platform is the os and arch a release file is signed for. macOS ships one
// universal binary.
func Platform() (goos, goarch string) {
	if runtime.GOOS == "darwin" {
		return "darwin", "universal"
	}
	return runtime.GOOS, runtime.GOARCH
}

// runTrial runs the downloaded binary's trial mode: one report with the
// real config, nothing saved.
func runTrial(ctx context.Context, bin, configPath string) (string, error) {
	var o commands.Output
	err := execx.Run(ctx, trialTimeout, &o, nil, bin, "trial", "--config", configPath)
	return o.String(), err
}

// sibling names the .new/.prev/.bad copies next to the binary, keeping
// .exe last on Windows so the copy can still be run.
func sibling(bin, tag string) string {
	ext := filepath.Ext(bin)
	if ext != ".exe" {
		ext = ""
	}
	return strings.TrimSuffix(bin, ext) + "." + tag + ext
}

// Cleanup removes the .new, .prev and .bad copies (for uninstall).
func Cleanup(bin string) {
	for _, tag := range []string{"new", "prev", "bad"} {
		_ = os.Remove(sibling(bin, tag))
	}
}
