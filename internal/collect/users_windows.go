package collect

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32                       = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformation = wtsapi32.NewProc("WTSQuerySessionInformationW")
)

const wtsUserName = 5

// users lists the accounts with an active or disconnected session.
func users(context.Context) []string {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		return []string{}
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	names := []string{}
	for _, s := range unsafe.Slice(sessions, count) {
		if s.State != windows.WTSActive && s.State != windows.WTSDisconnected {
			continue
		}
		if u := sessionString(s.SessionID, wtsUserName); u != "" {
			names = append(names, u)
		}
	}
	return uniqueSorted(names)
}

func sessionString(id uint32, class uint32) string {
	var buf *uint16
	var n uint32
	r, _, _ := procWTSQuerySessionInformation.Call(0, uintptr(id), uintptr(class),
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}
