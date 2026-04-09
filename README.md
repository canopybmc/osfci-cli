# osfci-cli

CLI client for [osfci.tech](https://osfci.tech), HPE's remote OpenBMC development platform (part of the [Open System Firmware CI](https://github.com/opencomputeproject/OSF-OSFCI) project). Allocate physical HPE ProLiant Gen11 servers, flash firmware, access serial consoles, and control power — all from the terminal.

## Install

```bash
go install github.com/canopybmc/osfci-cli@latest
```

Or build from source:

```bash
git clone https://github.com/canopybmc/osfci-cli
cd osfci-cli
go build -o osfci .
```

## Quick Start

```bash
# Log in with your HPE SSO or OSFCI credentials
osfci login

# See available servers
osfci server list

# Claim a server (60 min session)
osfci server claim DL320_GEN11

# Flash BMC firmware (waits for EM100 verify)
osfci flash firmware.static.mtd

# Load HPE's stock BIOS so the host can POST
osfci flash --original --bios

# Power on
osfci power on

# Watch the boot via serial console
osfci console --type bmc

# Open the BMC WebUI in your browser
osfci webui

# When done
osfci power off
osfci server release
```

## Commands

### Authentication

| Command | Description |
|---------|-------------|
| `osfci login` | Interactive login (HPE SSO and native accounts supported) |
| `osfci logout` | Clear saved session |

Session is stored at `~/.config/osfci-cli/session.json`.

### Server Management

| Command | Description |
|---------|-------------|
| `osfci server list` | List available server models |
| `osfci server claim <type>` | Allocate a server (e.g. `DL320_GEN11`, `DL325_GEN11`, `DL385_GEN11`) |
| `osfci server status` | Show allocated server, time remaining, BMC online/offline |
| `osfci server release` | Release the allocated server |

### Power Control

| Command | Description |
|---------|-------------|
| `osfci power on` | Power on the server |
| `osfci power off` | Power off the server |
| `osfci bmc status` | Check if BMC port 443 is reachable |

### Firmware Flashing

The OSFCI platform has **two separate EM100 SPI flash emulators** — one for the BMC and one for the host BIOS. Both must be loaded for the host to boot.

| Command | Description |
|---------|-------------|
| `osfci flash <file>` | Upload and flash BMC firmware |
| `osfci flash --bios <file>` | Upload and flash host BIOS firmware |
| `osfci flash --original` | Load HPE's stock BMC firmware |
| `osfci flash --original --bios` | Load HPE's stock BIOS firmware |
| `osfci flash --no-wait <file>` | Skip waiting for EM100 verification |
| `osfci emulator reset [bmc\|bios]` | Reset an EM100 emulator |
| `osfci emulator pool` | Check emulator pool status |

The `flash` command automatically waits for the EM100 to finish programming and verify the image before returning. You can safely run `power on` immediately after `flash` completes.

**Image format:** BMC images must be the `GXP2loader-*-sgn00.static.mtd` variant (exactly 32 MiB with the GXP ROM prepended). The raw `.static.mtd` will fail with a chip size mismatch.

### Serial Console

| Command | Description |
|---------|-------------|
| `osfci console` | Host serial console (EM100 output, default) |
| `osfci console --type bmc` | BMC serial console (UART — login shell) |
| `osfci console --type bios` | BIOS EM100 emulator console |
| `osfci console --type web` | BMC shell via bmcweb `/console0` WebSocket |
| `osfci console -f` | Follow mode (read-only, works without a TTY) |

Console types:

- **host** — EM100 emulator output. Shows firmware programming progress and U-Boot/kernel boot log.
- **bmc** — Physical UART serial console connected via FTDI adapter. Provides an interactive login shell on the BMC (`root` / `0penBmc`). Depends on the FTDI adapter being connected on the controller.
- **bios** — BIOS EM100 emulator output.
- **web** — Connects to the BMC's bmcweb `/console0` WebSocket, proxied through the gateway. Does **not** depend on the physical FTDI serial adapter. Requires the BMC to be booted.

Detach from an interactive console with `~.` (tilde-dot at start of line).

### BMC WebUI

```bash
osfci webui              # start proxy on https://localhost:8443
osfci webui --port 9443  # use a different port
```

Starts a local HTTPS reverse proxy that injects the CLI session cookie into all requests, giving you access to the full OpenBMC web interface (including SOL console and KVM). The upstream connection to osfci.tech is HTTPS; the local proxy uses a self-signed certificate.

Accept the certificate warning in your browser. In Chrome, type `thisisunsafe` on the warning page to permanently accept it (required for WebSocket connections like KVM and SOL to work).

### Logs

| Command | Description |
|---------|-------------|
| `osfci logs` | Download BMC SOL logs to `sol.log` |
| `osfci logs --bios` | Download BIOS SOL logs |
| `osfci logs -o boot.log` | Specify output file |

### OS Images (USB Boot)

OS images are written to a physical USB device attached to the server. Only pre-configured images from HPE's library are available.

| Command | Description |
|---------|-------------|
| `osfci os list` | List available OS images |
| `osfci os load <image>` | Download and write image to USB (shows dd progress) |
| `osfci os console` | Attach to the OS loader console |

```bash
osfci os list
osfci os load ubuntu-20.04-live-server-amd64.iso
osfci power on   # host boots from USB
```

## Typical Workflow

### Flash custom BMC + stock BIOS, boot host

```bash
osfci login
osfci server claim DL320_GEN11
osfci flash firmware.static.mtd          # custom BMC
osfci flash --original --bios            # HPE stock BIOS
osfci power on
osfci console --type bmc                 # watch BMC boot, login as root
# ~. to detach
osfci bmc status                         # wait for BMC to come online
osfci webui                              # open WebUI, Ctrl-C to stop proxy
osfci power off
osfci server release
```

### Boot stock firmware + install OS

```bash
osfci server claim DL325_GEN11
osfci flash --original                   # HPE stock BMC
osfci flash --original --bios            # HPE stock BIOS
osfci os load ubuntu-20.04-live-server-amd64.iso
osfci power on                           # boots from USB
osfci console --type bmc                 # monitor
osfci power off
osfci server release
```

### Check host power via Redfish (through gateway proxy)

```bash
curl -sk -u root:0penBmc \
  -b "osfci_cookie=$(jq -r .cookie ~/.config/osfci-cli/session.json)" \
  https://osfci.tech/redfish/v1/Systems/system | jq .PowerState

# Power on host via Redfish
curl -sk -u root:0penBmc \
  -b "osfci_cookie=$(jq -r .cookie ~/.config/osfci-cli/session.json)" \
  -X POST -H "Content-Type: application/json" \
  -d '{"ResetType":"On"}' \
  https://osfci.tech/redfish/v1/Systems/system/Actions/ComputerSystem.Reset
```

## Architecture

The CLI communicates with the OSFCI gateway at `osfci.tech`:

```
osfci-cli  --(HTTPS)--> osfci.tech gateway --(HTTP)--> controller node
                              |                              |
                              |  reverse proxy (port 443)    |  EM100 emulators
                              |                              |  FTDI serial adapters
                              +----> BMC (bmcweb/Redfish)    |  iPDU power control
                                                             |  USB storage
                                                             +----> target server
```

- **Gateway** — authenticates users, allocates servers, reverse-proxies BMC HTTPS and ttyd consoles
- **Controller** — manages EM100 flash emulators, serial adapters, power (iPDU), USB storage
- **BMC** — the OpenBMC instance running on the target server's GXP SoC

Authentication uses an `osfci_cookie` for session identification and HMAC-SHA1 signed requests for credential operations. HPE SSO (Okta) is handled server-side — the CLI just sends username/password.

## Server Instances

There are two OSFCI instances:

| Instance | Hostname | Flag |
|----------|----------|------|
| US (default) | `osfci.tech` | `--server osfci.tech` |
| Europe | `eu.osfci.tech` | `--server eu.osfci.tech` |

The CLI defaults to `osfci.tech`. To use the European instance, pass `--server` on your first command (login):

```bash
osfci --server eu.osfci.tech login
```

The server hostname is saved in the session file, so subsequent commands use it automatically. You do not need to pass `--server` after login.

Note: only one session is stored at a time. Logging in to a different instance overwrites the previous session.

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--server` | `osfci.tech` | OSFCI gateway hostname |

## Available Server Models

As of March 2026:

| Type | Brand |
|------|-------|
| `DL320_GEN11` | HPE |
| `DL325_GEN11` | HPE |
| `DL380_GEN11` | HPE |
| `DL385_GEN11` | HPE |
| `RL300_GEN11` | HPE |

## License

See repository root for license information.
