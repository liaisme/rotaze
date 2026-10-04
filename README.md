# Rotaze

**Self-healing SOCKS5 proxy with automatic IP rotation**

Rotaze is a lightweight Go tool that exposes a local SOCKS5 proxy (`127.0.0.1:1080`) and automatically rotates through free VPN tunnels when the target URL becomes unreachable.

## Features

- 🔄 **Automatic rotation** — Detects URL failures and switches to the next working tunnel
- 🌐 **Multiple protocols** — VLESS, Shadowsocks, Trojan, VMess, Hysteria2
- 🔌 **Transport layers** — WebSocket, gRPC, HTTPUpgrade for VLESS/VMess/Trojan
- 📡 **3,600+ nodes** — Fetches from 5 sources with concurrent fetching
- 🌍 **GeoIP enrichment** — Automatic country detection via ip-api.com
- 🎯 **Smart selection** — Prefers nodes with high success rates, skips bad nodes
- 🛡️ **SOCKS5 proxy** — Routes all traffic through sing-box
- 💾 **SQLite persistence** — Survives crashes, remembers state
- 📊 **Node stats** — Tracks latency, success rate per node
- 🔧 **CLI interface** — 12 commands for full control
- 🔄 **Background daemon** — Run as a persistent service (no systemd required)
- 🎮 **Interactive REPL** — Manual node control and exploration
- 🔃 **Config hot-reload** — Send SIGHUP to update interval/country without restart
- 🏗️ **Cross-platform** — Builds for Linux, macOS, Windows (amd64, arm64, armv7)

## Quick Start

```bash
# Build
make build

# One-shot check
./build/rotaze run --url https://example.com

# One-shot check using cached nodes (instant start, no fetch)
./build/rotaze run --cached --url https://example.com --check-ip --country VN

# Refresh + prune dead nodes once, then check
./build/rotaze pool refresh --prune && ./build/rotaze run --url https://example.com --check-ip --country VN

# Continuous monitoring with health + metrics endpoints
./build/rotaze watch --cached --url https://example.com --health-port 8788 --metrics :9091

# One-shot check using an OpenVPN config directly (no pool import)
./build/rotaze run --url https://example.com --ovpn /sdcard/Download/vpngate_vpn.ovpn --check-ip --country VN

# One-shot check on an OpenVPN node from the pool (--protocol ovpn)
./build/rotaze run --url https://example.com --protocol ovpn --check-ip --country VN

# List available nodes
./build/rotaze pool list --country VN

# Search nodes
./build/rotaze pool search "US"

# Show top nodes
./build/rotaze pool top -n 10

# Use a specific node
./build/rotaze use 5

# Use random node
./build/rotaze use --random

# Use best performing node
./build/rotaze use --best

# Lock to prevent auto-rotation
./build/rotaze lock on

# Show status
./build/rotaze status

# Interactive mode
./build/rotaze interactive

# Run as daemon
./build/rotaze daemon start --url https://example.com

# Run as daemon on an OpenVPN node from the pool
./build/rotaze daemon start --url https://example.com --protocol ovpn --country VN

# Run as daemon with an OpenVPN config directly (no pool import)
./build/rotaze daemon start --url https://example.com --ovpn /sdcard/Download/vpngate_vpn.ovpn
```

## Installation

### From source
```bash
git clone https://github.com/rotaze/rotaze.git
cd rotaze
make build
```

### Cross-compile
```bash
make cross-compile
# Builds for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
```

### Dependencies
- Go 1.25.5+
- sing-box library (embedded)

> On ARM devices (e.g. Raspberry Pi, Android boxes) be sure to install the **`linux-arm64`** build, not `linux-amd64`.

## Commands

| Command | Description |
|---------|-------------|
| `rotaze run --url <url>` | One-shot URL check via proxy |
| `rotaze watch --url <url>` | Continuous monitoring with auto-rotation |
| `rotaze pool refresh` | Fetch fresh nodes from sources |
| `rotaze pool refresh --prune` | Fetch + probe nodes, remove dead ones |
| `rotaze pool stats` | Show protocol/country breakdown |
| `rotaze pool search <query>` | Search by IP, tag, or country |
| `rotaze pool top` | Show top nodes ranked by score |
| `rotaze use <id>` | Switch to specific node by ID |
| `rotaze use --server <ip>` | Find node by server IP (partial match) |
| `rotaze use --tag <tag>` | Find node by sing-box tag (partial match) |
| `rotaze use --random` | Pick random node |
| `rotaze use --best` | Pick best performing node |
| `rotaze lock on/off` | Prevent/enable auto-rotation |
| `rotaze status` | Show current state and recent rotations |
| `rotaze interactive` | REPL mode for manual control |

## Configuration

Copy `config.example.toml` to `~/.rotaze/config.toml`:

```toml
[checker]
urls = ["https://httpbin.org/ip"]
interval = 30
max_retries = 3
timeout = 15
check_ip = true
country = "VN"
max_attempts = 3

[rotation]
strategy = "smart"
min_success_rate = 0.75
cooldown_period = 300

[proxy]
listen_port = 1080
listen_addr = "127.0.0.1"

[cache]
db_path = "~/.rotaze/state.db"
node_ttl = 3600
max_nodes = 200

[logging]
level = "info"
file = "~/.rotaze/rotaze.log"
stdout = true
```

### Environment Variables

```bash
ROTAZE_CHECKER_COUNTRY=US ./build/rotaze watch --url https://example.com
ROTAZE_LOG_LEVEL=debug ./build/rotaze run --url https://example.com
ROTAZE_PROXY_LISTEN_PORT=1081 ./build/rotaze run --url https://example.com
```

### Hot Reload

Send SIGHUP to running `watch` or `daemon` to reload config:

```bash
# Update interval/country without restart
kill -HUP $(pgrep rotaze)
```

## Usage Examples

### Use with curl
```bash
curl --proxy socks5://127.0.0.1:1080 https://httpbin.org/ip
```

### Use with Python
```python
import requests
proxies = {
    'http': 'socks5://127.0.0.1:1080',
    'https': 'socks5://127.0.0.1:1080'
}
r = requests.get('https://httpbin.org/ip', proxies=proxies)
print(r.json())
```

### Use with browsers (Firefox)
1. Go to Settings → Network Settings → Manual proxy
2. SOCKS Host: `127.0.0.1`, Port: `1080`
3. Select "SOCKS v5"

### Systemd service
```bash
sudo cp systemd/rotaze.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable rotaze
sudo systemctl start rotaze
```

### Troubleshooting: "address already in use" / stale sing-box

If a previous `rotaze` run crashed or was killed, its sing-box child can stay
behind and hold the proxy port (default `127.0.0.1:1080`). Since v0.x, rotaze
auto-detects this before starting: it kills the stale sing-box process holding
the port and retries once, so `run`/`watch`/`daemon` recover by themselves. If
the port is held by an unrelated process, rotaze reports a clear error instead
of silently talking to the wrong process. As a manual fallback:

```bash
pkill -9 -f sing-box
```

## Project Structure

```
rotaze/
├── cmd/rotaze/main.go               # Entry point
├── internal/
│   ├── cache/                       # SQLite state management
│   ├── checker/                     # URL monitoring + retry + SOCKS5
│   ├── cli/                         # 12 Cobra commands + REPL
│   ├── config/                      # TOML config loading
│   ├── fetcher/                     # Node fetching + URI parsing + GeoIP
│   ├── logger/                      # Zap structured logging
│   ├── rotator/                     # sing-box process management + config
│   └── version/                     # Build version info
├── pkg/types/                       # Shared data types
├── systemd/rotaze.service           # Systemd unit file
├── config.example.toml              # Example configuration
├── Makefile                         # Build/test/cross-compile targets
└── README.md                        # This file
```

## Testing

```bash
# Run all tests
make test

# Run with coverage
make test-cover

# Run with race detector
make test-race

# Run specific package tests
go test ./internal/fetcher/... -v
go test ./internal/cache/... -v
go test ./internal/checker/... -v
go test ./internal/rotator/... -v
```

## License

MIT License
