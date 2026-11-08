package commands

import (
	"bufio"
	"context"
	"strings"

	"github.com/marcpope/cortendesk-agent/internal/execx"
)

// wuScript installs every pending software update through the Windows
// Update Agent: no drivers, no hidden updates, nothing that asks the user
// a question. It ends with REBOOT=0 or REBOOT=1.
const wuScript = `
$s = New-Object -ComObject Microsoft.Update.Session
$s.ClientApplicationID = 'CortenDesk Agent'
"Searching for updates..."
$r = $s.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
$c = New-Object -ComObject Microsoft.Update.UpdateColl
foreach ($u in $r.Updates) {
  if ($u.InstallationBehavior.CanRequestUserInput) { "Skipped (needs user input): " + $u.Title; continue }
  if (-not $u.EulaAccepted) { $u.AcceptEula() }
  [void]$c.Add($u)
  "Found: " + $u.Title
}
if ($c.Count -eq 0) { "No updates to install."; "REBOOT=0"; exit 0 }
"Downloading " + $c.Count + " update(s)..."
$d = $s.CreateUpdateDownloader(); $d.Updates = $c; [void]$d.Download()
"Installing..."
$i = $s.CreateUpdateInstaller(); $i.Updates = $c; $res = $i.Install()
$codes = @{0='not started';1='in progress';2='succeeded';3='succeeded with errors';4='failed';5='aborted'}
for ($k = 0; $k -lt $c.Count; $k++) { $c.Item($k).Title + ": " + $codes[[int]$res.GetUpdateResult($k).ResultCode] }
"REBOOT=" + [int]$res.RebootRequired
if ($res.ResultCode -ne 2 -and $res.ResultCode -ne 3) { exit 2 }
`

func installUpdates(ctx context.Context, out *Output) (bool, error) {
	if err := execx.PowerShell(ctx, osUpdateTimeout, out, wuScript); err != nil {
		return false, err
	}
	return rebootFlag(out.String()), nil
}

func rebootFlag(s string) bool {
	sc := bufio.NewScanner(strings.NewReader(s))
	reboot := false
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "REBOOT=") {
			reboot = line == "REBOOT=1"
		}
	}
	return reboot
}
