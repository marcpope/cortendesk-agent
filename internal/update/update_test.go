package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
	"testing"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/commands"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type rig struct {
	t        *testing.T
	dir      string
	bin      string
	key      ed25519.PrivateKey
	store    *commands.Store
	files    map[string][]byte
	url      string
	restarts int
	trial    func(bin string) (string, error)
}

func newRig(t *testing.T) *rig {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	dir := t.TempDir()
	r := &rig{t: t, dir: dir, bin: filepath.Join(dir, "cortendesk-agent"), key: priv, files: map[string][]byte{}}
	r.store = commands.OpenStore(filepath.Join(dir, "commands.json"), quiet)
	if err := os.WriteFile(r.bin, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if b, ok := r.files[req.URL.Path]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	// The trial reads the binary it is given, like the real one would.
	r.trial = func(bin string) (string, error) {
		b, err := os.ReadFile(bin)
		if err != nil {
			return "", err
		}
		v, ok := strings.CutPrefix(string(b), "NEW ")
		if !ok {
			return "crashed", errors.New("exit status 2")
		}
		return "trial ok " + v + "\n", nil
	}
	return r
}

func (r *rig) updater(current string) *Updater {
	return &Updater{
		Binary: r.bin, Dir: r.dir, ConfigPath: filepath.Join(r.dir, "config.json"), Current: current,
		Keys: []ed25519.PublicKey{r.key.Public().(ed25519.PublicKey)}, Store: r.store, Log: quiet,
		Trial:   func(_ context.Context, bin, _ string) (string, error) { return r.trial(bin) },
		Restart: func() { r.restarts++ },
		GOOS:    "linux", GOARCH: "amd64",
	}
}

// publish serves body as version and returns signed params.
func (r *rig) publish(version, body string) map[string]string {
	path := "/agent-" + version
	r.files[path] = []byte(body)
	sum := sha256.Sum256([]byte(body))
	hexsum := hex.EncodeToString(sum[:])
	return map[string]string{"version": version, "url": r.url + path, "sha256": hexsum, "signature": Sign(r.key, version, "linux", "amd64", hexsum)}
}

func (r *rig) run(u *Updater, p map[string]string) (error, string) {
	b, _ := json.Marshal(p)
	var out commands.Output
	ctx := context.Background()
	return u.Run(withID(ctx, "cmd1"), b, &out), out.String()
}

// withID runs through a real commands.Runner so the handler sees the id.
func withID(ctx context.Context, id string) context.Context {
	var got context.Context
	r := &commands.Runner{Log: quiet, Store: commands.OpenStore("", quiet), Post: func(context.Context, string, api.CommandResult) error { return nil },
		Handlers: map[string]commands.Spec{"x": {Timeout: time.Hour, Run: func(c context.Context, _ json.RawMessage, _ *commands.Output) error {
			got = context.WithoutCancel(c)
			return nil
		}}}}
	r.Offer(ctx, []api.Command{{ID: id, Type: "x"}})
	r.Drain(ctx)
	return got
}

func (r *rig) read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestSignatureBindsVersionAndPlatform(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}
	sum := strings.Repeat("a", 64)
	sig := Sign(priv, "0.2.0", "windows", "amd64", sum)
	if !Verify(keys, "0.2.0", "windows", "amd64", sum, sig) {
		t.Fatal("valid signature rejected")
	}
	for _, c := range [][4]string{
		{"0.2.1", "windows", "amd64", sum},
		{"0.2.0", "linux", "amd64", sum},
		{"0.2.0", "windows", "arm64", sum},
		{"0.2.0", "windows", "amd64", strings.Repeat("b", 64)},
	} {
		if Verify(keys, c[0], c[1], c[2], c[3], sig) {
			t.Errorf("signature accepted for %v", c)
		}
	}
	if Verify(nil, "0.2.0", "windows", "amd64", sum, sig) || Verify(keys, "0.2.0", "windows", "amd64", sum, "bad") {
		t.Error("accepted without keys or with junk")
	}
}

func TestReleaseKeyIsCompiledIn(t *testing.T) {
	if len(Keys()) == 0 {
		t.Fatal("no release public key compiled in")
	}
}

func TestUpdateSwapsConfirmsAndReports(t *testing.T) {
	r := newRig(t)
	err, out := r.run(r.updater("0.1.0"), r.publish("0.2.0", "NEW 0.2.0"))
	if !errors.Is(err, commands.ErrResultLater) || r.restarts != 1 {
		t.Fatalf("err %v restarts %d\n%s", err, r.restarts, out)
	}
	if r.read(r.bin) != "NEW 0.2.0" || r.read(sibling(r.bin, "prev")) != "OLD" || r.read(sibling(r.bin, "new")) != "<missing>" {
		t.Fatalf("files: bin %q prev %q", r.read(r.bin), r.read(sibling(r.bin, "prev")))
	}

	// The service manager starts the new binary.
	g := &Guard{Dir: r.dir, Binary: r.bin, Current: "0.2.0", Store: r.store, Log: quiet, Restart: func() { r.restarts++ }}
	if g.Start() {
		t.Fatal("rolled back on first start")
	}
	g.Confirm()
	if _, ok := loadRecord(r.dir); ok {
		t.Fatal("update.json left after confirm")
	}
	p := r.store.Pending()
	if len(p) != 1 || p[0].ID != "cmd1" || p[0].Result.Status != api.StatusSucceeded || !strings.Contains(p[0].Result.Output, "0.1.0 to 0.2.0") {
		t.Fatalf("pending %+v", p)
	}
	// Watch returns at once once confirmed.
	done := make(chan struct{})
	go func() { g.Watch(context.Background(), time.Hour); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Watch kept waiting after confirm")
	}
}

func TestUpdateRefusesBadInput(t *testing.T) {
	r := newRig(t)
	u := r.updater("0.2.0")
	good := r.publish("0.3.0", "NEW 0.3.0")
	with := func(k, v string) map[string]string {
		p := map[string]string{}
		for a, b := range good {
			p[a] = b
		}
		p[k] = v
		return p
	}
	other := r.publish("0.3.1", "NEW 0.3.1")
	for name, p := range map[string]map[string]string{
		"older":          r.publish("0.1.9", "NEW 0.1.9"),
		"same":           r.publish("0.2.0", "NEW 0.2.0"),
		"bad signature":  with("signature", other["signature"]),
		"bad version":    with("version", "0.3"),
		"http to remote": with("url", "http://example.com/agent"),
		"wrong sha":      with("sha256", strings.Repeat("0", 64)),
	} {
		if err, _ := r.run(u, p); err == nil || errors.Is(err, commands.ErrResultLater) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	// A signed file whose bytes were swapped on the server.
	r.files["/agent-0.3.0"] = []byte("NEW 0.3.0 evil")
	if err, _ := r.run(u, good); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("tampered download: %v", err)
	}
	if r.read(r.bin) != "OLD" || r.restarts != 0 {
		t.Fatalf("binary touched: %q, restarts %d", r.read(r.bin), r.restarts)
	}
	entries, _ := os.ReadDir(r.dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".new") {
			t.Errorf("left %s behind", e.Name())
		}
	}
}

func TestTrialFailureChangesNothing(t *testing.T) {
	r := newRig(t)
	err, out := r.run(r.updater("0.1.0"), r.publish("0.2.0", "garbage"))
	if err == nil || !strings.Contains(err.Error(), "trial run") || !strings.Contains(out, "crashed") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if r.read(r.bin) != "OLD" || r.restarts != 0 || r.read(sibling(r.bin, "prev")) != "<missing>" {
		t.Fatal("binary changed after a failed trial")
	}
	if _, ok := loadRecord(r.dir); ok {
		t.Fatal("update.json written")
	}
}

// A new binary that keeps crashing is rolled back on its fourth start, and
// the old binary reports the failure and refuses that version.
func TestRollbackAfterCrashLoop(t *testing.T) {
	r := newRig(t)
	r.run(r.updater("0.1.0"), r.publish("0.2.0", "NEW 0.2.0"))

	for i := 1; i <= MaxStarts; i++ {
		g := &Guard{Dir: r.dir, Binary: r.bin, Current: "0.2.0", Store: r.store, Log: quiet}
		if g.Start() {
			t.Fatalf("rolled back on start %d", i)
		}
	}
	g := &Guard{Dir: r.dir, Binary: r.bin, Current: "0.2.0", Store: r.store, Log: quiet}
	if !g.Start() {
		t.Fatal("no rollback after too many starts")
	}
	if r.read(r.bin) != "OLD" || r.read(sibling(r.bin, "bad")) != "NEW 0.2.0" {
		t.Fatalf("after rollback: bin %q bad %q", r.read(r.bin), r.read(sibling(r.bin, "bad")))
	}

	old := &Guard{Dir: r.dir, Binary: r.bin, Current: "0.1.0", Store: r.store, Log: quiet}
	if old.Start() {
		t.Fatal("old binary rolled back again")
	}
	p := r.store.Pending()
	if len(p) != 1 || p[0].Result.Status != api.StatusFailed || !strings.Contains(p[0].Result.Error, "rolled back to 0.1.0") {
		t.Fatalf("pending %+v", p)
	}
	if !r.store.IsBad("0.2.0") {
		t.Fatal("0.2.0 not refused")
	}
	if err, _ := r.run(r.updater("0.1.0"), r.publish("0.2.0", "NEW 0.2.0")); err == nil || !strings.Contains(err.Error(), "failed to start") {
		t.Fatalf("bad version retried: %v", err)
	}
	if _, ok := loadRecord(r.dir); ok {
		t.Fatal("update.json left")
	}
}

// A new binary that starts but never gets a report through is rolled back
// when the watch runs out.
func TestRollbackWhenNoReportGetsThrough(t *testing.T) {
	r := newRig(t)
	r.run(r.updater("0.1.0"), r.publish("0.2.0", "NEW 0.2.0"))
	restarted := make(chan struct{})
	g := &Guard{Dir: r.dir, Binary: r.bin, Current: "0.2.0", Store: r.store, Log: quiet, Restart: func() { close(restarted) }}
	g.Start()
	go g.Watch(context.Background(), 20*time.Millisecond)
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("no rollback")
	}
	rec, _ := loadRecord(r.dir)
	if rec.Status != statusRolledBack || !strings.Contains(rec.Reason, "no report") || r.read(r.bin) != "OLD" {
		t.Fatalf("record %+v bin %q", rec, r.read(r.bin))
	}
	// Confirm after the rollback does nothing.
	g.Confirm()
	if len(r.store.Pending()) != 0 {
		t.Fatal("late confirm reported success")
	}
}

func TestSiblingNames(t *testing.T) {
	if got := sibling(`C:\Program Files\CortenDesk Agent\cortendesk-agent.exe`, "new"); got != `C:\Program Files\CortenDesk Agent\cortendesk-agent.new.exe` {
		t.Error(got)
	}
	if got := sibling("/usr/local/bin/cortendesk-agent", "prev"); got != "/usr/local/bin/cortendesk-agent.prev" {
		t.Error(got)
	}
}
