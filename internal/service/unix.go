//go:build !windows

package service

import "os"

func IsAdmin() bool { return os.Geteuid() == 0 }

func RemoveFile(path string) error { return removeNow(path) }
