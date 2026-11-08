package execx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// psPrelude makes errors quiet and output UTF-8 without a BOM.
const psPrelude = `$ErrorActionPreference='SilentlyContinue';$ProgressPreference='SilentlyContinue';` +
	`[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding $false;`

func powershell() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	}
	return "powershell.exe"
}

// PowerShell runs a fixed script and streams its output to w. Errors stop
// the script.
func PowerShell(ctx context.Context, timeout time.Duration, w io.Writer, script string) error {
	prelude := `$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue';` +
		`[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding $false;`
	return Run(ctx, timeout, w, nil, powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", prelude+script)
}

// PowerShellJSON runs a fixed script and decodes its JSON output into out.
// Scripts are constants: nothing from the console is ever put in them.
func PowerShellJSON(ctx context.Context, timeout time.Duration, script string, out any) error {
	b, err := Output(ctx, timeout, powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psPrelude+script)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes.TrimPrefix(bytes.TrimSpace(b), []byte("\xef\xbb\xbf")), out)
}
