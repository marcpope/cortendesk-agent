package service

import "golang.org/x/sys/windows"

const Name = "CortenDeskAgent"

var dependencies []string

// IsAdmin reports whether the process token is elevated.
func IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// RemoveFile deletes path, or schedules it for deletion at the next reboot
// when Windows holds it open (the running exe).
func RemoveFile(path string) error {
	if err := removeNow(path); err == nil {
		return nil
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}
