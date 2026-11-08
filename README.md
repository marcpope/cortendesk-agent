# CortenDesk Agent

A small service that reports machine health and inventory to a
[CortenDesk](https://github.com/marcpope/cortendesk) console. It reads CPU,
memory, disks, network, uptime, logged-in users, reboot state, RustDesk
state, and once a day hardware and software inventory. Every 30 minutes it
also runs slower read-only checks (antivirus, firewall, disk encryption,
pending updates, SMART, services the console asks it to watch, event log
errors, Wi-Fi network). It opens no listening ports.

> Very early, for testing with CortenDesk 2.0 beta. Not recommended for
> production.

The agent is completely optional. CortenDesk, its ID server, relay, web client
and RustDesk clients all work without it. Install it only on machines you want
to monitor, after turning on Monitoring in the console.

## Install

Use **Monitoring > Add agent** in the CortenDesk console. It gives you a
one-line install command for Windows, macOS and Linux with your server and
enrollment token filled in, plus manual download steps.

Windows builds are signed (publisher Marcus Pope). macOS builds are signed
with a Developer ID and notarized. Runs as SYSTEM on Windows and as root on
macOS and Linux.

```
cortendesk-agent install --server https://console.example.com --token cde_... [--ca-file ca.pem] [--no-commands]
cortendesk-agent status
cortendesk-agent uninstall
cortendesk-agent version
```

`--ca-file` trusts a private certificate authority. There is no option to
skip certificate checks. Plain `http://` works only when the console is on a
private network (10.x, 172.16-31.x, 192.168.x, 100.64-127.x, fc00::/7);
traffic over http is not encrypted, so use it only on a network you trust.

## Commands from the console

The agent runs a short, fixed list of commands, and nothing else: no
scripts, no shell. The console only queues them when an administrator has
allowed remote commands.

| Command | What it does |
|---|---|
| `rustdesk.upgrade` | Installs an official RustDesk release. The agent builds the GitHub download URL itself and checks the sha256 before the installer runs. |
| `os.update` | Installs pending OS updates, and reboots afterwards if asked and needed. |
| `reboot` | Schedules a reboot with an optional message. |
| `agent.update` | Updates the agent itself. |

Install with `--no-commands` to refuse everything except agent updates.

## Updates

The agent updates itself from signed releases. Every update is checked
against an Ed25519 signature and its sha256 before it runs, and must make a
successful test report first. If the new version can't reach the console
after it starts, the agent puts the previous version back and reports the
failure.

## Files

| OS | Binary | Config | Log |
|---|---|---|---|
| Windows | `C:\Program Files\CortenDesk Agent\cortendesk-agent.exe` | `C:\ProgramData\CortenDesk Agent\config.json` | `C:\ProgramData\CortenDesk Agent\agent.log` |
| macOS | `/Library/CortenDesk Agent/cortendesk-agent` | `/Library/Application Support/CortenDesk Agent/config.json` | `/Library/Logs/CortenDesk Agent/agent.log` |
| Linux | `/usr/local/bin/cortendesk-agent` | `/etc/cortendesk-agent/config.json` | journald |

The config is readable by SYSTEM and Administrators only on Windows, and by
root only elsewhere.

## Building from source

```
make test
make dist VERSION=0.2.0
```

Builds are static (`CGO_ENABLED=0`). Self-update only ever installs
officially signed releases.

## Licence

AGPL-3.0. See LICENSE, and NOTICE for third-party modules.
