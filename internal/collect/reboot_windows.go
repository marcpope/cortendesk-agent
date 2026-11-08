package collect

import "golang.org/x/sys/windows/registry"

// rebootPending checks the usual three places Windows records a pending
// restart: servicing, Windows Update and queued file renames.
func rebootPending() bool {
	if keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) ||
		keyExists(`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`) {
		return true
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringsValue("PendingFileRenameOperations")
	return err == nil && len(v) > 0
}

func keyExists(path string) bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	k.Close()
	return true
}
