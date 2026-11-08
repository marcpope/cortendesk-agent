package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/download"
	"github.com/marcpope/cortendesk-agent/internal/identity"
)

// RustDeskReleases is where installers come from. The agent builds the
// download URL itself, so the console can only choose which official
// release to install.
const RustDeskReleases = "https://github.com/rustdesk/rustdesk/releases/download/"

const (
	rustdeskTimeout  = 30 * time.Minute
	rustdeskMaxBytes = 300 << 20
	// How long the installed version gets to read back as the new one.
	rustdeskSettle = 5 * time.Minute
)

var (
	rdVersionRe = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	sha256Re    = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type rustdeskParams struct {
	Version string `json:"version"`
	Assets  []struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	} `json:"assets"`
}

// RustDesk holds what the upgrade needs from the machine. Tests replace
// the parts that touch it.
type RustDesk struct {
	// WorkDir holds downloads; only root/SYSTEM can write there.
	WorkDir string
	// BaseURL defaults to RustDeskReleases.
	BaseURL string
	// Version reads the installed RustDesk version.
	Version func(ctx context.Context) string
	// Candidates lists asset names this machine can install, best first.
	Candidates func(version string) []string
	// Install runs the installer at path (asset is its release name).
	Install func(ctx context.Context, path, asset string, out *Output) error
	// Poll is how often the version is re-read after installing.
	Poll time.Duration
}

func (rd *RustDesk) defaults() {
	if rd.BaseURL == "" {
		rd.BaseURL = RustDeskReleases
	}
	if rd.Version == nil {
		rd.Version = func(ctx context.Context) string { return identity.RustDesk(ctx).Version }
	}
	if rd.Candidates == nil {
		rd.Candidates = rustdeskCandidates
	}
	if rd.Install == nil {
		rd.Install = installRustDesk
	}
	if rd.Poll == 0 {
		rd.Poll = 5 * time.Second
	}
}

// Upgrade installs the requested RustDesk release. It is also a downgrade
// when the console asks for an older version.
func (rd *RustDesk) Upgrade(ctx context.Context, params json.RawMessage, out *Output) error {
	rd.defaults()
	var p rustdeskParams
	if err := decode(params, &p); err != nil {
		return err
	}
	if !rdVersionRe.MatchString(p.Version) {
		return fmt.Errorf("bad version %q", truncate(p.Version, 32))
	}
	offered := map[string]string{}
	for _, a := range p.Assets {
		if sha256Re.MatchString(a.SHA256) {
			offered[a.Name] = strings.ToLower(a.SHA256)
		}
	}
	wanted := rd.Candidates(p.Version)
	if len(wanted) == 0 {
		return fmt.Errorf("RustDesk has no installer for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var asset, sum string
	for _, name := range wanted {
		if s, ok := offered[name]; ok {
			asset, sum = name, s
			break
		}
	}
	if asset == "" {
		return fmt.Errorf("the release has none of the installers this machine needs (%s)", strings.Join(wanted, ", "))
	}

	before := rd.Version(ctx)
	if before == p.Version {
		out.Printf("RustDesk %s is already installed.", p.Version)
		return nil
	}

	if err := os.MkdirAll(rd.WorkDir, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(rd.WorkDir, "rustdesk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, asset)
	url := rd.BaseURL + p.Version + "/" + asset
	out.Printf("Downloading %s", url)
	if err := download.File(ctx, url, file, sum, rustdeskMaxBytes); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	out.Printf("sha256 %s verified.", sum)

	if before == "" {
		out.Printf("Installing RustDesk %s.", p.Version)
	} else {
		out.Printf("Replacing RustDesk %s with %s.", before, p.Version)
	}
	if err := rd.Install(ctx, file, asset, out); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// Some installers return before they finish; wait for the version.
	deadline := time.Now().Add(rustdeskSettle)
	for {
		got := rd.Version(ctx)
		if got == p.Version {
			out.Printf("RustDesk %s is installed.", got)
			return nil
		}
		if time.Now().After(deadline) {
			if got == "" {
				return errors.New("RustDesk is not installed after the installer ran")
			}
			return fmt.Errorf("RustDesk still reports %s after the installer ran", got)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rd.Poll):
		}
	}
}

// rdArch is RustDesk's name for this CPU, or "" when it has no build.
func rdArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return ""
}

// assetsFor fills the version and arch into RustDesk's release file names.
func assetsFor(version string, patterns ...string) []string {
	arch := rdArch()
	if arch == "" {
		return nil
	}
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		name := strings.NewReplacer("{v}", version, "{arch}", arch).Replace(p)
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
