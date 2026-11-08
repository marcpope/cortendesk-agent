package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/client"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type posts struct {
	mu   sync.Mutex
	got  map[string][]api.CommandResult
	fail error
}

func (p *posts) post(_ context.Context, id string, r api.CommandResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	if p.got == nil {
		p.got = map[string][]api.CommandResult{}
	}
	p.got[id] = append(p.got[id], r)
	return nil
}

func (p *posts) final(id string) api.CommandResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	rs := p.got[id]
	if len(rs) == 0 {
		return api.CommandResult{}
	}
	return rs[len(rs)-1]
}

func newRunner(t *testing.T, p *posts, handlers map[string]Spec) *Runner {
	return &Runner{Log: quiet, Store: OpenStore(filepath.Join(t.TempDir(), "commands.json"), quiet), Handlers: handlers, Post: p.post}
}

func TestRunnerRunsOnceReportsAndRetries(t *testing.T) {
	ran := 0
	p := &posts{}
	r := newRunner(t, p, map[string]Spec{
		"ok": {Timeout: time.Second, Run: func(ctx context.Context, _ json.RawMessage, out *Output) error {
			ran++
			out.Printf("hello %s", IDFrom(ctx))
			return nil
		}},
		"boom":  {Timeout: time.Second, Run: func(context.Context, json.RawMessage, *Output) error { return errors.New("broke") }},
		"panic": {Timeout: time.Second, Run: func(context.Context, json.RawMessage, *Output) error { panic("oops") }},
		"slow":  {Timeout: 10 * time.Millisecond, Run: func(ctx context.Context, _ json.RawMessage, _ *Output) error { <-ctx.Done(); return ctx.Err() }},
	})
	if got := strings.Join(r.Capabilities(), ","); got != "boom,ok,panic,slow" {
		t.Fatalf("capabilities %s", got)
	}
	ctx := context.Background()
	r.Offer(ctx, []api.Command{{ID: "a", Type: "ok"}, {ID: "a", Type: "ok"}, {ID: "b", Type: "boom"}, {ID: "c", Type: "rm -rf"}, {ID: "d", Type: "panic"}, {ID: "e", Type: "slow"}})
	r.Drain(ctx)
	// Redelivery of a seen id does nothing.
	r.Offer(ctx, []api.Command{{ID: "a", Type: "ok"}})
	r.Drain(ctx)

	if ran != 1 {
		t.Fatalf("ok ran %d times", ran)
	}
	if got := p.got["a"]; len(got) != 2 || got[0].Status != api.StatusRunning || got[1].Status != api.StatusSucceeded || got[1].Output != "hello a\n" {
		t.Fatalf("a: %+v", got)
	}
	if f := p.final("b"); f.Status != api.StatusFailed || f.Error != "broke" {
		t.Errorf("b: %+v", f)
	}
	if f := p.final("c"); f.Status != api.StatusFailed || !strings.Contains(f.Error, "does not run") || len(p.got["c"]) != 1 {
		t.Errorf("c: %+v", p.got["c"])
	}
	if f := p.final("d"); f.Status != api.StatusFailed || !strings.Contains(f.Error, "oops") {
		t.Errorf("d: %+v", f)
	}
	if f := p.final("e"); f.Status != api.StatusFailed || !strings.Contains(f.Error, "timed out") {
		t.Errorf("e: %+v", f)
	}

	// Console down: the result is kept and sent by Flush later.
	p.fail = errors.New("down")
	r.Offer(ctx, []api.Command{{ID: "f", Type: "ok"}})
	r.Drain(ctx)
	if n := len(r.Store.Pending()); n != 1 {
		t.Fatalf("pending %d", n)
	}
	// A reopened store still has it (agent restarted).
	r.Store = OpenStore(r.Store.path, quiet)
	p.fail = nil
	r.Flush(ctx)
	if f := p.final("f"); f.Status != api.StatusSucceeded || len(r.Store.Pending()) != 0 {
		t.Fatalf("f after flush: %+v, pending %d", f, len(r.Store.Pending()))
	}
	// A result the console refuses (command deleted) is dropped.
	r.Store.AddPending("gone", api.CommandResult{Status: api.StatusSucceeded})
	p.fail = &client.APIError{Status: 404}
	r.Flush(ctx)
	if n := len(r.Store.Pending()); n != 0 {
		t.Fatalf("404 result kept: %d", n)
	}
}

func TestSeenListIsCapped(t *testing.T) {
	s := OpenStore("", quiet)
	for i := range seenMax + 10 {
		s.MarkSeen(string(rune('a'+i%26)) + strings.Repeat("x", i))
	}
	if len(s.d.Seen) != seenMax {
		t.Fatalf("seen %d", len(s.d.Seen))
	}
}

func TestBadVersionsExpire(t *testing.T) {
	s := OpenStore("", quiet)
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.MarkBad("0.2.0")
	if !s.IsBad("0.2.0") || s.IsBad("0.3.0") {
		t.Fatal("IsBad")
	}
	now = now.Add(25 * time.Hour)
	if s.IsBad("0.2.0") {
		t.Fatal("still bad after a day")
	}
}

func TestOutputKeepsHeadAndTail(t *testing.T) {
	var o Output
	o.Write([]byte("start\n"))
	o.Write([]byte(strings.Repeat("a", 3*MaxOutput)))
	o.Write([]byte("end"))
	s := o.String()
	if !strings.HasPrefix(s, "start\n") || !strings.HasSuffix(s, "end") || !strings.Contains(s, "\n...\n") || len(s) != MaxOutput+5 {
		t.Fatalf("len %d", len(s))
	}
	var short Output
	short.Printf("one %d", 1)
	if short.String() != "one 1\n" {
		t.Fatalf("%q", short.String())
	}
}

func TestRebootParams(t *testing.T) {
	d, msg, err := parseReboot(json.RawMessage(`{"delay": 0, "message": "Back in 5 \"min\"\u0007"}`))
	if err != nil || d != minRebootDelay || msg != "Back in 5 min" {
		t.Fatalf("%v %q %v", d, msg, err)
	}
	d, _, _ = parseReboot(json.RawMessage(`{"delay": 600}`))
	if d != 10*time.Minute {
		t.Fatal(d)
	}
	for _, bad := range []string{`{"delay": -1}`, `{"delay": 90000}`, `{"delay": 1, "command": "x"}`, `[]`} {
		if _, _, err := parseReboot(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	long := strings.Repeat("é", 300)
	if _, msg, _ := parseReboot(json.RawMessage(`{"delay": 60, "message": "` + long + `"}`)); len([]rune(msg)) != maxRebootMessage {
		t.Errorf("message not cut: %d", len([]rune(msg)))
	}
}

// rustdeskRig serves fake installers over loopback http.
type rustdeskRig struct {
	rd        *RustDesk
	installed string
	installs  []string
	files     map[string][]byte
}

func newRustDeskRig(t *testing.T) *rustdeskRig {
	g := &rustdeskRig{installed: "1.5.0", files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := g.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	g.rd = &RustDesk{
		WorkDir: t.TempDir(),
		BaseURL: srv.URL + "/dl/",
		Version: func(context.Context) string { return g.installed },
		Candidates: func(v string) []string {
			return []string{"rustdesk-" + v + "-x86_64.msi", "rustdesk-" + v + "-x86_64.exe"}
		},
		Install: func(_ context.Context, path, asset string, out *Output) error {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			g.installs = append(g.installs, asset+":"+string(b))
			g.installed = strings.Split(asset, "-")[1]
			return nil
		},
		Poll: time.Millisecond,
	}
	return g
}

func (g *rustdeskRig) serve(version, asset, body string) string {
	g.files["/dl/"+version+"/"+asset] = []byte(body)
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func TestRustDeskUpgrade(t *testing.T) {
	g := newRustDeskRig(t)
	ctx := context.Background()
	exe := g.serve("1.5.1", "rustdesk-1.5.1-x86_64.exe", "EXE")
	params := func(v string, assets ...[2]string) json.RawMessage {
		var a []map[string]string
		for _, x := range assets {
			a = append(a, map[string]string{"name": x[0], "sha256": x[1]})
		}
		b, _ := json.Marshal(map[string]any{"version": v, "assets": a})
		return b
	}

	// Picks the first candidate the release has (no msi offered), checks
	// the hash, installs, reads the version back.
	var out Output
	err := g.rd.Upgrade(ctx, params("1.5.1", [2]string{"rustdesk-1.5.1-aarch64.dmg", exe}, [2]string{"rustdesk-1.5.1-x86_64.exe", exe}), &out)
	if err != nil || len(g.installs) != 1 || g.installs[0] != "rustdesk-1.5.1-x86_64.exe:EXE" {
		t.Fatalf("err %v installs %v\n%s", err, g.installs, out.String())
	}
	entries, _ := os.ReadDir(g.rd.WorkDir)
	if len(entries) != 0 {
		t.Errorf("download left behind: %v", entries)
	}

	// Already on that version: nothing to do.
	if err := g.rd.Upgrade(ctx, params("1.5.1", [2]string{"rustdesk-1.5.1-x86_64.exe", exe}), &Output{}); err != nil || len(g.installs) != 1 {
		t.Fatalf("reinstalled: %v %v", err, g.installs)
	}

	// Wrong hash: refused before the installer runs.
	g.installed = "1.5.0"
	bad := strings.Repeat("0", 64)
	if err := g.rd.Upgrade(ctx, params("1.5.1", [2]string{"rustdesk-1.5.1-x86_64.exe", bad}), &Output{}); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") || len(g.installs) != 1 {
		t.Fatalf("bad hash: %v %v", err, g.installs)
	}

	for name, p := range map[string]json.RawMessage{
		"bad version":    params("1.5.1; rm", [2]string{"rustdesk-1.5.1-x86_64.exe", exe}),
		"path in name":   params("1.5.1", [2]string{"../../evil.exe", exe}),
		"no fit":         params("1.5.1", [2]string{"rustdesk-1.5.1-aarch64.dmg", exe}),
		"unknown field":  json.RawMessage(`{"version":"1.5.1","assets":[],"url":"https://evil"}`),
		"not an object":  json.RawMessage(`"1.5.1"`),
		"bad sha format": params("1.5.1", [2]string{"rustdesk-1.5.1-x86_64.exe", "zz"}),
	} {
		if err := g.rd.Upgrade(ctx, p, &Output{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(g.installs) != 1 {
		t.Fatalf("an invalid command installed something: %v", g.installs)
	}
}

func TestRustDeskVersionMustReadBack(t *testing.T) {
	g := newRustDeskRig(t)
	g.rd.Install = func(context.Context, string, string, *Output) error { return nil } // silently does nothing
	sum := g.serve("1.5.1", "rustdesk-1.5.1-x86_64.exe", "EXE")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := g.rd.Upgrade(ctx, json.RawMessage(`{"version":"1.5.1","assets":[{"name":"rustdesk-1.5.1-x86_64.exe","sha256":"`+sum+`"}]}`), &Output{})
	if err == nil {
		t.Fatal("succeeded without the version changing")
	}
}

func TestAssetNames(t *testing.T) {
	got := assetsFor("1.5.1", "rustdesk-{v}-{arch}.exe", "rustdesk-{v}-0.{arch}-suse.rpm")
	if len(got) != 2 || !strings.HasPrefix(got[0], "rustdesk-1.5.1-") || !strings.HasSuffix(got[1], "-suse.rpm") {
		t.Fatal(got)
	}
	for _, name := range rustdeskCandidates("1.5.1") {
		if !strings.HasPrefix(name, "rustdesk-1.5.1-") {
			t.Errorf("candidate %q", name)
		}
	}
}
