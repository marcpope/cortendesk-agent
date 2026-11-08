# CortenDesk Agent

A small service that reports machine health and inventory to a CortenDesk
console. It reads CPU, memory, disks, network, uptime, logged-in users,
reboot state, RustDesk state, and once a day hardware and software
inventory. Every 30 minutes it also runs slower read-only checks
(antivirus, firewall, disk encryption, pending updates, SMART, services the
console asks it to watch, event log errors, Wi-Fi network) and sends the
cached result with each report. It opens no listening ports.

It runs a short, fixed list of commands the console can queue, and nothing
else (no scripts, no shell):

| Command | What it does |
|---|---|
| `rustdesk.upgrade` | Installs the requested official RustDesk release. The download URL is built by the agent (`github.com/rustdesk/rustdesk/releases/download/...`); the console only picks the version and supplies the sha256, which must match before the installer runs. |
| `os.update` | Installs pending OS updates (Windows Update Agent, `softwareupdate`, apt/dnf/yum/zypper), and reboots afterwards if asked and needed. |
| `reboot` | Schedules a reboot with an optional message. |
| `agent.update` | Self-update, see below. |

Commands run one at a time in the background, so reports keep going. Each
command id runs once. Results the console doesn't accept are kept in
`commands.json` and sent again after the next good report. Install with
`--no-commands` to refuse everything except `agent.update`.

Service names to watch come from the console. Anything outside letters,
digits, spaces and `$ . - _ @ ( )` is dropped, and names are only passed
as single arguments or to the Windows service manager, never through a
shell.

One static Go binary per OS and architecture. Runs as SYSTEM (Windows) or
root (macOS, Linux).

## Commands

```
cortendesk-agent install --server https://console.example.com --token cde_... [--ca-file ca.pem] [--no-commands]
cortendesk-agent uninstall
cortendesk-agent run [--config FILE] [--foreground]
cortendesk-agent status [--config FILE]
cortendesk-agent version
cortendesk-agent trial [--config FILE]   # used by self-update
```

`install` enrolls with the console first, then copies the binary into place,
writes the config and starts the service. Running it again with a new token
re-enrolls the same machine and restarts the service.

`--ca-file` adds a private CA to the system trust store. There is no option
to skip certificate checks.

## Files

| OS | Binary | Config | Log |
|---|---|---|---|
| Windows | `C:\Program Files\CortenDesk Agent\cortendesk-agent.exe` | `C:\ProgramData\CortenDesk Agent\config.json` | `C:\ProgramData\CortenDesk Agent\agent.log` |
| macOS | `/Library/CortenDesk Agent/cortendesk-agent` | `/Library/Application Support/CortenDesk Agent/config.json` | `/Library/Logs/CortenDesk Agent/agent.log` |
| Linux | `/usr/local/bin/cortendesk-agent` | `/etc/cortendesk-agent/config.json` | journald |

The config directory is readable by SYSTEM and Administrators only on
Windows, and by root only (0700/0600) elsewhere. `state.json` next to the
config holds the last report result and inventory hash. Log files rotate at
5 MB with 3 old files kept.

A config file may hold `server` and `token` without `agent_id`. The agent
then enrolls on its first run and replaces the token with its credentials.

## Self-update

The console's manifest names the version, URL, sha256 and Ed25519 signature
for each platform. The signature covers
`cortendesk-agent\n<version>\n<os>/<arch>\n<sha256>\n` and is checked
against the public key compiled into `internal/update/sign.go`. Older or
equal versions are refused.

1. Download next to the binary as `cortendesk-agent.new`, check signature
   and sha256.
2. Run `cortendesk-agent.new trial`: one report with the real config,
   nothing written. It must succeed within 2 minutes, or nothing changes.
3. Move the running binary to `.prev`, the new one into place, write
   `update.json`, exit with code 3. The service manager starts the new
   binary.
4. The new binary commits after its first report the console answers, and
   reports success.
5. If it starts 3 times without that, or 10 minutes pass, `.prev` goes back
   into place and the old binary reports the failure and refuses that
   version for 24 hours.

## Release signing

```
go run ./tools/release-sign keygen -out agent-signing.key   # once; keep the key offline
make dist VERSION=0.2.0
tools/sign-macos.sh dist/cortendesk-agent_darwin_universal  # on the Mac with the Developer ID key
# Windows: run the "Sign Windows build" workflow with the same version and
# replace dist/*.exe with its artifact
make manifest VERSION=0.2.0    # needs CORTENDESK_AGENT_SIGNING_KEY or -key
```

Code signing comes before `make manifest`, because it changes the bytes the
manifest hashes. Windows builds are signed with Azure Artifact Signing
(publisher Marcus Pope), macOS builds with a Developer ID and notarized.

`make manifest` prints the block for the console's
`resources/agent/manifest.json`. `sign` refuses a key whose public half is
not compiled in.

## Local development

No root needed. Start the fake console, write a config next to its CA, run
the agent in the foreground:

```
mkdir -p /tmp/agent-dev
go run ./tools/fakeconsole -addr 127.0.0.1:8443 -token cde_dev -ca-out /tmp/agent-dev/ca.pem
echo '{"server":"https://127.0.0.1:8443","token":"cde_dev","ca_file":"ca.pem"}' > /tmp/agent-dev/config.json
go run ./cmd/cortendesk-agent run --config /tmp/agent-dev/config.json --foreground
```

Plain `http://` is accepted only when the console is on a private network:
loopback, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10 (carrier-grade
NAT, Tailscale) or fc00::/7. The address is checked on every connection, so a
name that resolves to a public address is refused. Over http the enrollment
token and the agent's key travel unencrypted; use it only on a network you trust.

## Build

```
make test
make dist VERSION=0.2.0   # Windows and Linux amd64/arm64, macOS universal (needs lipo), SHA256SUMS
```

Builds use `CGO_ENABLED=0`.

## Inventory hash

`hash` is the hex sha256 of the inventory body without the `hash` key,
encoded as canonical JSON: object keys sorted at every level, no whitespace,
no HTML escaping, numbers as sent. A console can verify it by decoding the
body, removing `hash`, sorting keys recursively and re-encoding.

## Licence

AGPL-3.0. See LICENSE, and NOTICE for third-party modules.
