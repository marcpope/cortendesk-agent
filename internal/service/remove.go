package service

import (
	"errors"
	"io/fs"
	"os"
)

func removeNow(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
