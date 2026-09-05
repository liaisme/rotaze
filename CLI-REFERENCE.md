# CLI-REFERENCE.md - Rotaze Command Reference

**Project**: Rotaze  
**Version**: 1.0  
**Date**: 2026-08-30

---

## Table of Contents
1. [Overview](#overview)
2. [Global Flags](#global-flags)
3. [Run Modes](#run-modes)
4. [Commands](#commands)
5. [Examples](#examples)
6. [Exit Codes](#exit-codes)
7. [Configuration](#configuration)

---

## Overview

**Rotaze** exposes a local SOCKS5 proxy (`127.0.0.1:1080`) and automatically rotates through free VPN tunnels when the target URL becomes unreachable.

### Quick Start
```bash
# One-shot check
./rotaze run --url https://example.com

# Continuous monitoring
./rotaze watch --url https://example.com

# Background daemon
./rotaze daemon start

# Interactive control
./rotaze interactive
```

---

## Global Flags

These flags work with all commands:

### `--config <path>`
Path to configuration file (TOML format).

**Default**: `~/.rotaze/config.toml`

**Example**:
```bash
rotaze run --config /etc/rotaze/config.toml --url https://example.com
```

---

### `--verbose` or `-v`
Enable verbose logging (debug level).

**Example**:
```bash
rotaze watch -v --url https://example.com
```

---

### `--log-file <path>`
Write logs to file instead of stdout.

**Default**: Stdout only (unless daemon mode)

**Example**:
```bash
rotaze run --log-file /tmp/rotaze.log --url https://example.com
```

---

### `--help` or `-h`
Show help text for any command.

**Example**:
```bash
rotaze --help
rotaze run --help
rotaze pool list --help
```

---

## Run Modes

### Mode 1: `rotaze run` - One-Shot Check

Check target URL once via proxy, exit with status code.

**Usage**:
```bash
rotaze run [flags]
```

**Flags**:
- `--url <url>` (required): Target URL to check
  - Example: `https://example.com`
  - Also checks: Endpoint reachability, HTTP status

- `--country <CC>` (optional): Country code for exit IP
  - Default: `VN` (Vietnam)
  - Examples: `US`, `JP`, `SG`, `all`

- `--check-ip` (optional): Verify exit IP country
  - Contacts: `https://ipinfo.io/json` **through the SOCKS5 proxy** (verifies the tunnel's exit IP, not your local IP)
  - Fails if exit IP country ≠ requested country
  - A node whose exit IP is the wrong country counts as a failed node — `run`
    **rotates to the next candidate** instead of aborting, and only exits 1
    after every candidate failed country verification
  - Default: false (no check)

- `--timeout <seconds>` (optional): Request timeout
  - Default: `15`
  - Range: 5-60 seconds

- `--cached` (optional): Use cached nodes only (skip fetching from sources)
  - Default: false (fetch fresh nodes first)

- `--attempts <n>` (optional): Max distinct nodes to try
  - Default: `3`
  - `0` = try all candidates in the filtered pool
  - Range: 0-100

**Behavior notes**:
- Candidate nodes come from the pool that matches `--country` (or your
  configured country). The selected node from `use` is honored when present in
  that pool; otherwise nodes are tried in preference order (SS with a supported
  cipher first).
- If the first node's URL check fails, `run` automatically rotates through
  distinct nodes before giving up (default 3, `--attempts` to change; `0` =
  all) — a dead node no longer fails the whole run.
- `-v`/`--verbose` adds `[debug]` lines for the candidate counts, each attempt,
  and prints the sing-box version + config path used.

**Examples**:

```bash
# Basic check
rotaze run --url https://example.com
→ Exit 0 if OK, 1 if failed

# Check with exit IP verification
rotaze run --url https://example.com --check-ip --country VN
→ Verifies URL accessible AND exit IP is Vietnam

# Check US exit
rotaze run --url https://example.com --country US --check-ip

# Skip fetching (use cached pool)
rotaze run --cached --url https://example.com --check-ip --country VN

# Strict timeout
rotaze run --url https://example.com --timeout 5

# Try every VN candidate before giving up
rotaze run --url https://toplinks.io/kSDVNrX --country VN --attempts 0 -v
```

**Output**:
```
[13:45:00] INFO    Fetching nodes for country VN...
[13:45:02] INFO    Fetched 127 nodes
[13:45:02] INFO    Tunnel up (vless-1.2.3.4:443)
[13:45:03] INFO    URL check: OK (200)
[13:45:03] INFO    Exit IP: 1.2.3.4 (VN) ✓
✓ Success
```

**Exit Codes**:
- `0`: URL accessible, country verified (if `--check-ip`)
- `1`: URL unreachable or country mismatch
- `2`: Configuration error
- `3`: No nodes available

---

### Mode 2: `rotaze watch` - Continuous Monitoring

Run in foreground, continuously check target URL, rotate on failure.

**Usage**:
```bash
rotaze watch [flags]
```

**Flags**:
- `--url <url>` (required): Target URL to monitor

- `--interval <seconds>` (optional): Check interval
  - Default: `30`
  - Range: 5-3600 seconds
  - How often to check URL

- `--timeout <seconds>` (optional): Request timeout per check
  - Default: `15`
  - Range: 5-60 seconds

- `--country <CC>` (optional): Exit IP country requirement
  - Default: `VN`
  - Examples: `US`, `JP`, `SG`, `all`

- `--check-ip` (optional): Verify exit IP country
  - Default: false

- `--attempts <N>` (optional): Consecutive failures before rotate
  - Default: `1`
  - Range: 1-5
  - Example: `--attempts 3` = fail 3 times, then rotate

- `--health-port <port>` (optional): Serve a JSON health endpoint on the exact port
  - Endpoints: `/health`, `/health/ready`, `/health/live`
  - Default: `0` (disabled). Previously ignored; now binds the exact port requested.

- `--metrics <addr>` (optional): Prometheus metrics endpoint (e.g. `:9090`)
  - Default: disabled

- `--cached` (optional): Use cached nodes only (skip fetching from sources)
  - Default: false

**Examples**:

```bash
# Basic watch (check every 30s)
rotaze watch --url https://example.com

# Strict: Fail once, rotate immediately
rotaze watch --url https://example.com --attempts 1 --check-ip

# Relaxed: Fail 3 times, then rotate
rotaze watch --url https://example.com --attempts 3

# Check every minute, verify country
rotaze watch --url https://example.com --interval 60 --check-ip --country US

# Start instantly from cached pool, with health + metrics endpoints
rotaze watch --cached --url https://example.com --health-port 8788 --metrics :9091
```

**Output** (real-time):
```
[13:45:00] INFO    Starting watch mode for https://example.com
[13:45:00] INFO    Current node: vless-1.2.3.4:443 (VN)
[13:45:30] INFO    URL check: OK (200) | exit 1.2.3.4 (VN) ✓
[13:46:00] INFO    URL check: OK (200) | exit 1.2.3.4 (VN) ✓
[13:46:30] WARN    URL check: FAIL (timeout)
[13:46:31] INFO    Retry 1/3: FAIL (timeout)
[13:46:33] INFO    Retry 2/3: OK (200) ✓
[13:47:00] INFO    URL check: OK (200) | exit 1.2.3.4 (VN) ✓
[13:47:30] ERROR   URL check: FAIL (403 Forbidden)
[13:47:31] INFO    Retry 1/3: FAIL (403)
[13:47:33] INFO    Retry 2/3: FAIL (403)
[13:47:37] INFO    Retry 3/3: FAIL (403)
[13:47:37] INFO    Rotating to next node...
[13:47:39] INFO    Rotated to: vless-5.6.7.8:443 (VN)
[13:47:40] INFO    URL check: OK (200) | exit 5.6.7.8 (VN) ✓
```

**Ctrl+C to stop**:
```
^C
[13:47:55] INFO    Received signal, shutting down...
[13:47:55] INFO    Closing sing-box...
✓ Shutdown complete
```

---

### Mode 3: `rotaze daemon` - Background Service

Run as background service (systemd-friendly), persistent state + logging.

**Usage**:
```bash
rotaze daemon <subcommand> [flags]
```

**Subcommands**:

#### `rotaze daemon start`
Start background daemon.

**Flags**:
- `--config <path>` (optional): Config file
  - Default: `~/.rotaze/config.toml`
  - If file missing: Use defaults

- `--log-file <path>` (optional): Log file path
  - Default: `~/.rotaze/rotaze.log`
  - Rotated daily

**Example**:
```bash
rotaze daemon start
rotaze daemon start --config /etc/rotaze/config.toml --log-file /var/log/rotaze.log
```

**Output**:
```
[13:45:00] INFO    Starting Rotaze daemon...
[13:45:00] INFO    Config: ~/.rotaze/config.toml
[13:45:00] INFO    Log file: ~/.rotaze/rotaze.log
[13:45:01] INFO    Database: ~/.rotaze/state.db
[13:45:01] INFO    Fetching nodes...
[13:45:03] INFO    Fetched 127 nodes (VN)
[13:45:03] INFO    SOCKS5 proxy listening on 127.0.0.1:1080
[13:45:03] INFO    Starting monitor loop (interval: 30s)
✓ Daemon started (PID: 1234)
```

---

#### `rotaze daemon stop`
Stop background daemon gracefully.

**Example**:
```bash
rotaze daemon stop
→ ✓ Daemon stopped
```

---

#### `rotaze daemon status`
Show daemon status.

**Example**:
```bash
rotaze daemon status

→ Output:
Status:       RUNNING
PID:          1234
Config:       ~/.rotaze/config.toml
Log:          ~/.rotaze/rotaze.log
Started:      2024-08-30 13:45:00
Uptime:       2h 15m
Current node: vless-1.2.3.4:443 (VN)
Last check:   5s ago ✓
```

---

#### `rotaze daemon logs`
View daemon logs (tail mode).

**Flags**:
- `--follow` or `-f`: Follow log (like `tail -f`)
  - Default: Show last 20 lines

- `--lines <N>`: Show last N lines
  - Default: `20`

**Example**:
```bash
# Show last 50 lines
rotaze daemon logs --lines 50

# Follow logs in real-time
rotaze daemon logs --follow
→ (Ctrl+C to stop)
```

---

#### `rotaze daemon restart`
Stop + start daemon (reload config).

**Example**:
```bash
rotaze daemon restart
→ ✓ Daemon restarted
```

---

### Mode 4: `rotaze interactive` - REPL Mode

Interactive shell for manual control and exploration.

**Usage**:
```bash
rotaze interactive
```

**Prompt**:
```
rotator>
```

**Commands Inside REPL**:

| Command | Description | Example |
|---------|-------------|---------|
| `list` | Show available nodes | `rotator> list --country VN` |
| `use` | Switch to node | `rotator> use 3` |
| `lock` | Lock/unlock to current | `rotator> lock on` |
| `status` | Show current state | `rotator> status` |
| `test-url` | Test URL via current | `rotator> test-url https://example.com` |
| `rotate` | Force rotation | `rotator> rotate` |
| `stats` | Pool statistics | `rotator> stats` |
| `help` | Show all commands | `rotator> help` |
| `exit` | Quit | `rotator> exit` |

**Example Session**:

```bash
$ rotaze interactive

rotator> list
ID   Server              Port  Protocol  Country  Latency  Success
─────────────────────────────────────────────────────────────────
1    1.2.3.4            443   VLESS     VN       45ms     95%
2    5.6.7.8            443   VLESS     VN       52ms     92%
3    9.10.11.12         443   SS        VN       38ms     98%  ✓ current
4    13.14.15.16        8388  SS        US       120ms    85%

rotator> use 2
✓ Switched to vless-5.6.7.8:443

rotator> status
Current: vless-5.6.7.8:443 (VN)
Status: ✓ OK (last check 2s ago)
Locked: NO
Pool: 127 nodes (112 working, 8 failed, 7 cooldown)

rotator> lock on
✓ Locked to vless-5.6.7.8:443 (auto-rotation disabled)

rotator> test-url https://example.com
Testing...
✓ OK (200) [latency: 123ms]

rotator> stats
Protocol breakdown:
  VLESS:  80 (63%)
  SS:     35 (28%)
  Trojan: 12 (9%)

Country breakdown:
  VN:     45 (35%)
  US:     40 (32%)
  SG:     25 (20%)
  JP:     17 (13%)

rotator> exit
✓ Goodbye!
```

---

## Commands

### `rotaze pool list` - List Available Nodes

Show all cached nodes with stats.

**Usage**:
```bash
rotaze pool list [flags]
```

**Flags**:
- `--country <CC>` (optional): Filter by country
  - Examples: `VN`, `US`, `JP`, `SG`, `all`

- `--protocol <proto>` (optional): Filter by protocol
  - Options: `vless`, `ss`, `trojan`, `vmess`, `hy2`

- `--sort <field>` (optional): Sort results
  - Options: `latency`, `success`, `random`, `id`
  - Default: `id`

**Examples**:

```bash
# List all nodes
rotaze pool list

# List Vietnam VLESS only
rotaze pool list --country VN --protocol vless

# List US nodes sorted by latency
rotaze pool list --country US --sort latency

# List all nodes sorted by success rate
rotaze pool list --sort success
```

**Output**:
```
ID   Server              Port  Protocol  Country  Latency  Success
─────────────────────────────────────────────────────────────────
1    1.2.3.4            443   VLESS     VN       45ms     95%
2    5.6.7.8            443   VLESS     VN       52ms     92%
3    9.10.11.12         443   SS        VN       38ms     98%
4    13.14.15.16        8388  SS        US       120ms    85%
```

---

### `rotaze use` - Manual Node Selection

Switch to specific node immediately.

**Usage**:
```bash
rotaze use <ID> [flags]
```

**Arguments**:
- `<ID>`: Node ID (from `pool list`)
  - Example: `rotaze use 3`

**Flags** (alternative to ID):
- `--server <IP>`: Select by server IP
  - Example: `rotaze use --server 1.2.3.4`

- `--tag <tag>`: Select by sing-box tag
  - Example: `rotaze use --tag "vless-1.2.3.4-443"`

- `--random`: Pick random node
  - Example: `rotaze use --random`

- `--best`: Pick best performer
  - Example: `rotaze use --best`

**Examples**:

```bash
rotaze use 3
→ ✓ Switched to ss-9.10.11.12:8388

rotaze use --server 1.2.3.4
→ ✓ Switched to vless-1.2.3.4:443

rotaze use --best
→ ✓ Switched to vless-9.10.11.12:443 (98% success)

rotaze use --random
→ ✓ Switched to trojan-5.6.7.8:443
```

---

### `rotaze lock` - Prevent Auto-Rotation

Lock to current node (disable auto-rotation) or unlock.

**Usage**:
```bash
rotaze lock <on|off|show>
```

**Subcommands**:
- `on`: Lock to current node
- `off`: Unlock (allow auto-rotation)
- `show`: Show lock status

**Examples**:

```bash
# Lock to current node
rotaze lock on
→ ✓ Locked to vless-1.2.3.4:443 (auto-rotation disabled)

# Unlock
rotaze lock off
→ ✓ Unlocked (auto-rotation enabled)

# Check status
rotaze lock show
→ Locked to: vless-1.2.3.4:443 (VN)
→ Auto-rotation: DISABLED
→ Last rotated: 2024-08-30 13:45:00
```

---

### `rotaze status` - Show Current State

Display current node, pool stats, recent rotations.

**Usage**:
```bash
rotaze status
```

**Output**:
```
Current Node:    vless-1.2.3.4:443 (VN)
Status:          ✓ OK (last check 5s ago)
Locked:          No
Lock state:      AUTO-ROTATION ENABLED

Pool Stats:
  Total nodes:   127
  Working:       112 (88%)
  Failed:        8
  Cooldown:      7

Recent Rotations:
  13:45 → vless-5.6.7.8:443 (manual, success)
  13:30 → vless-9.10.11.12:443 (failure, success)
  13:15 → trojan-1.2.3.4:443 (refresh, success)
```

---

### `rotaze pool stats` - Pool Statistics

Show protocol and country breakdown.

**Usage**:
```bash
rotaze pool stats
```

**Output**:
```
Protocol breakdown:
  VLESS:  80 (63%)
  SS:     35 (28%)
  Trojan: 12 (9%)

Country breakdown:
  VN:     45 (35%)
  US:     40 (32%)
  SG:     25 (20%)
  JP:     17 (13%)
```

---

### `rotaze config` - Configuration Management

Show, validate, generate, reload config.

**Usage**:
```bash
rotaze config <subcommand> [flags]
```

**Subcommands**:

#### `rotaze config generate`
Generate default config.

```bash
rotaze config generate > config.toml
→ Default config written to config.toml
```

---

#### `rotaze config show`
Show current config (as loaded).

```bash
rotaze config show

→ Output:
[checker]
  urls = ["https://toplinks.io/kSDVNrX"]
  interval = 30
  max_retries = 3
  timeout = 15
  check_ip = false
  country = "VN"
```

---

#### `rotaze config validate`
Validate config file.

```bash
rotaze config validate -f ~/.rotaze/config.toml
→ ✓ Config valid
```

---

#### `rotaze config reload`
Reload config from file (daemon must be running).

```bash
rotaze config reload
→ ✓ Config reloaded (no restart required)
```

---

## Examples

### Example 1: Developer Testing

```bash
# Start watch mode
rotaze watch --url https://api.mysite.com/health --interval 10

# In another terminal
for i in {1..100}; do
  curl --proxy socks5://127.0.0.1:1080 https://api.mysite.com/test
done

# Result: 100 requests from rotating IPs (via rotation if needed)
```

---

### Example 2: Production Deployment

```bash
# Install
sudo cp rotaze /usr/local/bin/
sudo mkdir -p /etc/rotaze /var/lib/rotaze
sudo cp config.toml /etc/rotaze/
sudo chown rotaze:rotaze /var/lib/rotaze

# Install systemd service
sudo cp rotaze.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable rotaze
sudo systemctl start rotaze

# Check status
rotaze daemon status
sudo systemctl status rotaze

# View logs
rotaze daemon logs --follow
```

---

### Example 3: Manual Node Exploration

```bash
rotaze interactive

rotator> list --country VN --sort latency
→ Shows Vietnam nodes sorted by latency

rotator> use 3
→ Switches to node 3

rotator> lock on
→ Locks to node 3 (prevent auto-rotation)

rotator> test-url https://example.com
→ Tests URL via current (locked) node

rotator> exit
```

---

### Example 4: One-Shot Check with Exit Code

```bash
#!/bin/bash

# Check if site accessible via Vietnam VPN
rotaze run --url https://example.com --country VN --check-ip

if [ $? -eq 0 ]; then
  echo "✓ Site accessible via VN VPN"
  exit 0
else
  echo "✗ Site not accessible"
  exit 1
fi
```

---

### Example 5: Cron Job (Periodic Check)

```bash
# /etc/cron.d/rotaze-check
# Run every 1 hour

0 * * * * rotaze run --url https://example.com --timeout 10 >> /var/log/rotaze-check.log 2>&1 && echo "✓ Check passed at $(date)" || echo "✗ Check failed at $(date)"
```

---

## Exit Codes

| Code | Meaning | Use Case |
|------|---------|----------|
| `0` | Success | URL accessible, all checks pass |
| `1` | Failure | URL unreachable, check failed |
| `2` | Config error | Invalid config, parsing failed |
| `3` | No nodes | No available tunnels in pool |
| `4` | sing-box error | Proxy startup failed |
| `5` | Port in use | 1080 already occupied |
| `6` | Interrupted | Ctrl+C / signal received |

---

## Configuration

### Config File Format (TOML)

**Location**: `~/.rotaze/config.toml` (default)

**Template**:
```toml
[checker]
urls = ["https://toplinks.io/kSDVNrX"]
interval = 30
max_retries = 3
timeout = 15
check_ip = false
country = "VN"

[rotation]
strategy = "smart"
min_success_rate = 0.75
cooldown_period = 300

[proxy]
listen_port = 1080

[cache]
db_path = "~/.rotaze/state.db"
node_ttl = 3600

[logging]
level = "info"
file = "~/.rotaze/rotaze.log"
```

### Environment Variable Overrides

```bash
ROTAZE_CHECKER_COUNTRY=US rotaze watch --url https://example.com
→ Uses US exit instead of config's VN

ROTAZE_CHECKER_INTERVAL=60 rotaze watch --url https://example.com
→ Check every 60s instead of config's 30s

ROTAZE_LOG_LEVEL=debug rotaze run --url https://example.com
→ Debug logging enabled
```

### CLI Flag Overrides

```bash
# CLI flags override both config and env vars
rotaze watch --url https://example.com --country US --interval 60
→ Uses US country and 60s interval (regardless of config/env)
```

---

## Tips & Tricks

### Redirect All Traffic Through Rotaze

```bash
# Use with curl
curl --proxy socks5://127.0.0.1:1080 https://example.com

# Use with wget
wget --proxy=on -e use_proxy=yes -e http_proxy=socks5://127.0.0.1:1080 https://example.com

# Use with Python requests
python3 << 'EOF'
import requests
proxies = {'http': 'socks5://127.0.0.1:1080', 'https': 'socks5://127.0.0.1:1080'}
r = requests.get('https://example.com', proxies=proxies)
print(r.status_code)
EOF

# Use with Firefox/Chrome
# Preferences → Network → Manual proxy configuration
# SOCKS5: 127.0.0.1:1080
```

---

### Debugging

```bash
# Verbose output
rotaze run -v --url https://example.com
→ Shows debug-level logs

# Save logs to file
rotaze watch --log-file /tmp/rotaze-debug.log --url https://example.com -v
→ Detailed logs for diagnosis

# Check DB directly (SQLite)
sqlite3 ~/.rotaze/state.db
> SELECT * FROM nodes LIMIT 5;
> SELECT * FROM state;
```

---

### Systemd Integration

```bash
# Check service status
systemctl status rotaze

# View service logs
journalctl -u rotaze -f

# Restart service
systemctl restart rotaze

# Check if service is running
systemctl is-active rotaze && echo "✓ Running" || echo "✗ Stopped"
```

---

**Last Updated**: 2026-08-30  
**Version**: 1.0

