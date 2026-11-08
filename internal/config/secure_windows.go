package config

import (
	"os"

	"golang.org/x/sys/windows"
)

// SYSTEM and Administrators full control, inherited by files, nothing
// inherited from ProgramData (which lets Users read).
const configSDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// SecureDir creates dir and replaces its ACL. Files written into it later
// inherit the same ACL.
func SecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(configSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}
