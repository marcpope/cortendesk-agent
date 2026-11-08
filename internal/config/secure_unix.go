//go:build !windows

package config

import "os"

// SecureDir creates dir readable by its owner only (root, for the service).
func SecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}
