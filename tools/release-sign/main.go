// Command release-sign makes the Ed25519 release key and signs builds for
// the console's resources/agent/manifest.json.
//
//	go run ./tools/release-sign keygen -out agent-signing.key
//	go run ./tools/release-sign sign -key agent-signing.key -version 0.2.0 -base-url URL dist/
//
// sign reads the key from -key or $CORTENDESK_AGENT_SIGNING_KEY (base64
// seed). Run it last: code signing and notarizing change the bytes.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/semver"
	"github.com/marcpope/cortendesk-agent/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: release-sign keygen|sign ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "file for the private key (base64 seed, mode 0600)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(priv.Seed()) + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Println("Private key written to", *out)
	fmt.Println("Public key (put it in internal/update/sign.go):")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

var nameRe = regexp.MustCompile(`^cortendesk-agent_(windows|darwin|linux)_(amd64|arm64|universal)(\.exe)?$`)

type file struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file (else $CORTENDESK_AGENT_SIGNING_KEY)")
	version := fs.String("version", "", "release version, e.g. 0.2.0")
	baseURL := fs.String("base-url", "", "release download URL (default: the GitHub release for -version)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !semver.Valid(*version) || fs.NArg() != 1 {
		return errors.New("usage: sign -version X.Y.Z [-key F] [-base-url URL] DIR")
	}
	seed := os.Getenv("CORTENDESK_AGENT_SIGNING_KEY")
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		seed = string(b)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seed))
	if err != nil || len(raw) != ed25519.SeedSize {
		return errors.New("no valid signing key")
	}
	key := ed25519.NewKeyFromSeed(raw)
	if !compiledIn(key) {
		return errors.New("this key's public half is not in internal/update/sign.go; agents would refuse the update")
	}
	if *baseURL == "" {
		*baseURL = "https://github.com/marcpope/cortendesk-agent/releases/download/v" + *version
	}

	dir := fs.Arg(0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	files := map[string]file{}
	for _, e := range entries {
		m := nameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		hexsum := hex.EncodeToString(sum[:])
		files[m[1]+"-"+m[2]] = file{Name: e.Name(), SHA256: hexsum, Signature: update.Sign(key, *version, m[1], m[2], hexsum)}
	}
	if len(files) == 0 {
		return fmt.Errorf("no cortendesk-agent_<os>_<arch> files in %s", dir)
	}
	out := struct {
		Version string          `json:"version"`
		BaseURL string          `json:"base_url"`
		Files   map[string]file `json:"files"`
	}{*version, *baseURL, files}
	b, _ := json.MarshalIndent(out, "", "    ")
	fmt.Println(string(b))
	return nil
}

func compiledIn(key ed25519.PrivateKey) bool {
	pub := key.Public().(ed25519.PublicKey)
	for _, k := range update.Keys() {
		if k.Equal(pub) {
			return true
		}
	}
	return false
}
