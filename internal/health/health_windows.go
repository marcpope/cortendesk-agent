package health

import (
	"context"
	"errors"
	"time"

	"github.com/marcpope/cortendesk-agent/internal/api"
	"github.com/marcpope/cortendesk-agent/internal/execx"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

var checks = []Check{
	{Key: "antivirus", Timeout: 2 * time.Minute, Run: antivirus},
	{Key: "firewall", Timeout: time.Minute, Run: firewall},
	{Key: "encryption", Timeout: 2 * time.Minute, Run: encryption},
	// The Windows Update search can take minutes. It runs in its own
	// goroutine, so a report never waits for it.
	{Key: "updates", Timeout: 5 * time.Minute, Run: updates},
	{Key: "smart", Timeout: 2 * time.Minute, Run: smart},
	{Key: "services", Timeout: 30 * time.Second, Run: services},
	{Key: "event_errors_24h", Timeout: 3 * time.Minute, Run: eventErrors},
	{Key: "wifi_ssid", Timeout: 15 * time.Second, Run: wifi},
}

// All scripts below are constants. Values from the console never reach
// PowerShell.

// SecurityCenter2 exists on workstations only; servers fall back to
// Defender. productState: byte 2 is 0x10/0x11 when on, byte 3 is 0x00
// when signatures are current.
const avScript = `$p=@(Get-CimInstance -Namespace root/SecurityCenter2 -ClassName AntiVirusProduct | ForEach-Object {` +
	`$s=[int]$_.productState;[pscustomobject]@{name=[string]$_.displayName;enabled=((($s -shr 12) -band 0xF) -eq 1);up_to_date=((($s -shr 4) -band 0xF) -eq 0)}});` +
	`if($p.Count -eq 0){$m=Get-MpComputerStatus;if($m){$p=@([pscustomobject]@{name='Microsoft Defender Antivirus';enabled=[bool]$m.AntivirusEnabled;up_to_date=(-not [bool]$m.DefenderSignaturesOutOfDate)})}};` +
	`ConvertTo-Json -InputObject @($p) -Compress`

func antivirus(ctx context.Context, _ []string, out *api.Health) error {
	var r []struct {
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
		UpToDate bool   `json:"up_to_date"`
	}
	if err := execx.PowerShellJSON(ctx, 2*time.Minute, avScript, &r); err != nil {
		return err
	}
	for _, a := range r {
		if a.Name != "" {
			out.Antivirus = append(out.Antivirus, api.Antivirus{Name: a.Name, Enabled: ptr(a.Enabled), UpToDate: ptr(a.UpToDate)})
		}
	}
	return nil
}

// True only when the Domain, Private and Public profiles are all on.
// NotConfigured means the default, which is on.
const fwScript = `$p=@(Get-NetFirewallProfile -ErrorAction Stop);if($p.Count -lt 3){exit 1};` +
	`ConvertTo-Json -InputObject (@($p | Where-Object {[string]$_.Enabled -eq 'False'}).Count -eq 0) -Compress`

func firewall(ctx context.Context, _ []string, out *api.Health) error {
	var on bool
	if err := execx.PowerShellJSON(ctx, time.Minute, fwScript, &on); err != nil {
		return err
	}
	out.Firewall = ptr(on)
	return nil
}

// VolumeType 2 is removable. A volume counts as encrypted when fully
// converted or when protection is on.
const encScript = `$v=@(Get-CimInstance -Namespace root/cimv2/Security/MicrosoftVolumeEncryption -ClassName Win32_EncryptableVolume -ErrorAction Stop |` +
	` Where-Object {$_.DriveLetter -and $_.VolumeType -ne 2} | ForEach-Object {` +
	`$e=(([int]$_.ConversionStatus -eq 1) -or ([int]$_.ProtectionStatus -eq 1));[pscustomobject]@{volume=[string]$_.DriveLetter;encrypted=$e}});` +
	`ConvertTo-Json -InputObject @($v) -Compress`

func encryption(ctx context.Context, _ []string, out *api.Health) error {
	var r []api.Encryption
	if err := execx.PowerShellJSON(ctx, 2*time.Minute, encScript, &r); err != nil {
		return err
	}
	for _, e := range r {
		if e.Encrypted {
			e.Method = "BitLocker"
		}
		out.Encryption = append(out.Encryption, e)
	}
	return nil
}

// History: Operation 1 is install; ResultCode 2/3 succeeded, 4 failed.
const updScript = `$s=New-Object -ComObject Microsoft.Update.Session;$q=$s.CreateUpdateSearcher();` +
	`$r=$q.Search('IsInstalled=0 and IsHidden=0');$missing=[int]$r.Updates.Count;` +
	`$n=$q.GetTotalHistoryCount();$h=@();if($n -gt 0){$h=@($q.QueryHistory(0,[Math]::Min($n,500)))};` +
	`$cut=(Get-Date).AddDays(-30);` +
	`$failed=@($h | Where-Object {$_.Date -ge $cut -and $_.Operation -eq 1 -and $_.ResultCode -eq 4}).Count;` +
	`$last=$h | Where-Object {$_.Operation -eq 1 -and ($_.ResultCode -eq 2 -or $_.ResultCode -eq 3)} | Sort-Object Date -Descending | Select-Object -First 1;` +
	`$li='';if($last){$li=$last.Date.ToString('yyyy-MM-dd')};` +
	`[pscustomobject]@{missing=$missing;failed=[int]$failed;last_installed=$li} | ConvertTo-Json -Compress`

func updates(ctx context.Context, _ []string, out *api.Health) error {
	var r struct {
		Missing       int    `json:"missing"`
		Failed        int    `json:"failed"`
		LastInstalled string `json:"last_installed"`
	}
	if err := execx.PowerShellJSON(ctx, 5*time.Minute, updScript, &r); err != nil {
		return err
	}
	out.Updates = &api.Updates{Missing: ptr(r.Missing), Failed: ptr(r.Failed), LastInstalled: r.LastInstalled}
	return nil
}

const smartScript = `$f=@(Get-CimInstance -Namespace root/wmi -ClassName MSStorageDriver_FailurePredictStatus);` +
	`if($f.Count -gt 0){$r=@($f | ForEach-Object {$b=[bool]$_.PredictFailure;[pscustomobject]@{device=[string]$_.InstanceName;model='';healthy=(-not $b);status=$(if($b){'Pred Fail'}else{'OK'})}})}` +
	`else{$r=@(Get-PhysicalDisk | ForEach-Object {[pscustomobject]@{device=('\\.\PHYSICALDRIVE'+$_.DeviceId);model=[string]$_.FriendlyName;healthy=([string]$_.HealthStatus -eq 'Healthy');status=[string]$_.HealthStatus}})};` +
	`ConvertTo-Json -InputObject @($r) -Compress`

func smart(ctx context.Context, _ []string, out *api.Health) error {
	var r []api.Smart
	if err := execx.PowerShellJSON(ctx, 2*time.Minute, smartScript, &r); err != nil {
		return err
	}
	out.Smart = r
	return nil
}

const evScript = `$t=(Get-Date).AddDays(-1);$n=0;foreach($l in 'System','Application'){` +
	`$n+=@(Get-WinEvent -FilterHashtable @{LogName=$l;Level=1,2;StartTime=$t} -MaxEvents 10000).Count};` +
	`ConvertTo-Json -InputObject $n -Compress`

func eventErrors(ctx context.Context, _ []string, out *api.Health) error {
	var n int
	if err := execx.PowerShellJSON(ctx, 3*time.Minute, evScript, &n); err != nil {
		return err
	}
	out.EventErrors24h = ptr(n)
	return nil
}

func wifi(ctx context.Context, _ []string, out *api.Health) error {
	b, err := execx.Output(ctx, 15*time.Second, "netsh", "wlan", "show", "interfaces")
	if err != nil {
		return err
	}
	out.WifiSSID = ParseNetshSSID(string(b))
	return nil
}

// services reads the service control manager directly with query-only
// rights; the names never go near a command line.
func services(_ context.Context, watch []string, out *api.Health) error {
	if len(watch) == 0 {
		return nil
	}
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return err
	}
	m := &mgr.Mgr{Handle: h}
	defer m.Disconnect()
	for _, name := range watch {
		out.Services = append(out.Services, queryService(h, name))
	}
	return nil
}

func queryService(scm windows.Handle, name string) api.Service {
	res := api.Service{Name: name, State: api.ServiceOther}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return res
	}
	sh, err := windows.OpenService(scm, p, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			res.State = api.ServiceMissing
		}
		return res
	}
	s := &mgr.Service{Name: name, Handle: sh}
	defer s.Close()
	if cfg, err := s.Config(); err == nil {
		res.Display = cfg.DisplayName
	}
	st, err := s.Query()
	if err != nil {
		return res
	}
	switch st.State {
	case svc.Running:
		res.State = api.ServiceRunning
	case svc.Stopped:
		res.State = api.ServiceStopped
	}
	return res
}
