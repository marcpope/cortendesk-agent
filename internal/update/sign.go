// Package update replaces the agent binary with a newer signed release and
// rolls back when the new one can't report (docs/monitoring/SPEC.md,
// "Agent self-update").
package update

import (
	"crypto/ed25519"
	"encoding/base64"
)

// releaseKeys are the Ed25519 public keys release builds are signed with,
// base64. The private key lives only in the release workflow's secrets and
// the maintainer's password manager. A second key can be added here ahead of
// a rotation.
var releaseKeys = []string{
	"BbpR3vQribM79lsPxDn3R2T2FTSFW4FHeWGsgB9oxfs=", // 2026-11-07
}

// Keys returns the compiled-in public keys.
func Keys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, k := range releaseKeys {
		b, err := base64.StdEncoding.DecodeString(k)
		if err == nil && len(b) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(b))
		}
	}
	return out
}

// Message is what a release signature covers. Binding version and platform
// stops a valid signature being replayed for another build.
func Message(version, goos, goarch, sha256hex string) []byte {
	return []byte("cortendesk-agent\n" + version + "\n" + goos + "/" + goarch + "\n" + sha256hex + "\n")
}

// Sign signs one build. Used by tools/release-sign.
func Sign(key ed25519.PrivateKey, version, goos, goarch, sha256hex string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, Message(version, goos, goarch, sha256hex)))
}

// Verify checks sig (base64) against any of keys.
func Verify(keys []ed25519.PublicKey, version, goos, goarch, sha256hex, sig string) bool {
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	msg := Message(version, goos, goarch, sha256hex)
	for _, k := range keys {
		if ed25519.Verify(k, msg, b) {
			return true
		}
	}
	return false
}
