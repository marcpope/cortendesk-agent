package commands

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

const uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// The MSI and the exe installer don't upgrade each other's installs, so
// stay with whichever put RustDesk there.
func rustdeskCandidates(version string) []string {
	if rustdeskFromMSI() {
		return assetsFor(version, "rustdesk-{v}-{arch}.msi")
	}
	return assetsFor(version, "rustdesk-{v}-{arch}.exe")
}

// rustdeskFromMSI looks for an uninstall entry named RustDesk that Windows
// Installer owns.
func rustdeskFromMSI() bool {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstallKey, registry.ENUMERATE_SUB_KEYS|registry.WOW64_64KEY)
	if err != nil {
		return false
	}
	defer root.Close()
	names, _ := root.ReadSubKeyNames(-1)
	for _, n := range names {
		if !strings.HasPrefix(n, "{") {
			continue
		}
		k, err := registry.OpenKey(root, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name, _, _ := k.GetStringValue("DisplayName")
		msi, _, _ := k.GetIntegerValue("WindowsInstaller")
		k.Close()
		if name == "RustDesk" && msi == 1 {
			return true
		}
	}
	return false
}

func installRustDesk(ctx context.Context, path, asset string, out *Output) error {
	if strings.HasSuffix(asset, ".msi") {
		msiexec := filepath.Join(os.Getenv("SystemRoot"), "System32", "msiexec.exe")
		log := path + ".log"
		err := execx.Run(ctx, rustdeskTimeout, out, nil, msiexec, "/i", path, "/qn", "/norestart", "/l*", log)
		// 3010: installed, reboot needed to finish.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 3010 {
			out.Printf("Windows Installer asks for a reboot to finish.")
			return nil
		}
		return err
	}
	return execx.Run(ctx, rustdeskTimeout, out, nil, path, "--silent-install")
}
