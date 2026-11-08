package identity

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// One Toolhelp snapshot instead of opening every process.
func processRunning(names []string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		n := windows.UTF16ToString(e.ExeFile[:])
		for _, want := range names {
			if strings.EqualFold(n, want) {
				return true
			}
		}
	}
	return false
}
