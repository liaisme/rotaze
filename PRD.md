# PRD.md - Rotaze Product Requirements Document

**Project Name**: Rotaze  
**Version**: 1.0 (MVP)  
**Status**: In Development  
**Author**: [Development Team]  
**Date**: 2026-08-30

---

## 1. Executive Summary

**Rotaze** is a self-healing SOCKS5 proxy server that automatically rotates through free VPN tunnels when the target website becomes unreachable. It exposes a local SOCKS5 proxy (`127.0.0.1:1080`) that applications can connect to, while a background monitoring process checks a user-specified target URL. When the URL fails to respond, Rotaze intelligently selects the next best-performing tunnel and switches traffic through it—all without disrupting existing connections.

### Problem Statement
Users need a reliable way to maintain continuous internet access through rotating IP addresses, particularly for:
- Accessing geo-blocked content
- Bypassing rate limits
- Testing applications with multiple IPs
- Web scraping with IP rotation

Existing solutions require:
- Manual tunnel switching (tedious)
- External VPN binaries (heavy, complex)
- Complex infrastructure (server + external proxy)

### Solution
Rotaze combines:
- **Lightweight**: Single Go binary (~60-80MB), no external dependencies
- **Embedded**: sing-box compiled in (no binary download, no subprocess overhead)
- **Intelligent**: Smart node selection based on performance metrics
- **User-friendly**: Simple CLI, manual control, REPL mode
- **Reliable**: SQLite state persistence, graceful recovery

---

## 2. Product Vision & Goals

### Vision
A simple, standalone tool that provides "set it and forget it" SOCKS5 proxy with automatic IP rotation, enabling users to maintain connectivity through unavailable proxies without manual intervention.

### Primary Goals
1. **Automatic failover**: Detect URL unreachability, rotate to next IP within 10 seconds
2. **No manual management**: Fetch, parse, and manage tunnel nodes automatically
3. **Smart selection**: Prefer reliable tunnels, avoid consistently-failing ones
4. **Easy control**: Simple CLI to list, pick, lock specific tunnels
5. **Production-ready**: Runs as systemd service, survives crashes, persistent state

### Success Metrics (MVP)
- ✅ URL availability: 95%+ uptime via rotation
- ✅ Rotation time: <10s from failure to new IP
- ✅ Node pool: 50+ working tunnels at any time
- ✅ Performance: <100ms SOCKS5 latency overhead
- ✅ Memory: <100MB idle, <200MB under load
- ✅ CPU: <5% idle, <20% during rotation

---

## 3. Target Users

### Primary
1. **Developers**: Testing applications with multiple IPs, load testing
2. **Security researchers**: Analyzing services from different geolocations
3. **Web scrapers**: Maintaining access during rate limiting
4. **DevOps engineers**: Building resilient infrastructure components

### Secondary
1. **System administrators**: Deploying as edge service
2. **Privacy-conscious users**: Rotating IPs automatically
3. **Network engineers**: Testing failover scenarios

### User Personas

**Persona 1: Developer (David)**
- Uses for load testing and testing multi-IP scenarios
- Wants: Simple CLI, works out-of-box, no configuration
- Uses: `rotaze watch --url https://api.example.com`

**Persona 2: DevOps (Sarah)**
- Deploys as systemd service in production
- Wants: Reliable, persistent state, metrics, logging
- Uses: `rotaze daemon start`, systemd monitoring

**Persona 3: Power User (Alex)**
- Wants to understand & control node selection
- Wants: Manual picking, performance stats, interactive mode
- Uses: `rotaze interactive`, `rotaze pool list`, `rotaze use 5`

---

## 4. Functional Requirements

### 4.1 Node Management

#### FR-1.1: Fetch Nodes from Multiple Sources
**Requirement**: Rotaze shall fetch tunnel configurations from 3 independent sources.

**Details**:
- **Au1rxx free-vpn-subscriptions** (GitHub repository)
  - Format: sing-box JSON outbounds
  - URL: `https://raw.githubusercontent.com/Au1rxx/free-vpn-subscriptions/main/output/...`
  - Coverage: 50-200+ VLESS nodes globally
  - Update frequency: Daily automatic

- **mahdibland SSAggregator** (merged base64 pool)
  - Format: Base64-encoded URI lines
  - URL: `https://raw.githubusercontent.com/mahdibland/SSAggregator/master/...`
  - Coverage: 200+ nodes (mixed protocols)
  - Contains: VMess, Shadowsocks, Trojan, VLESS

- **ConfigForge ShatakVPN** (per-country lists)
  - Format: Plaintext URI lines
  - URL: `https://raw.githubusercontent.com/ShatakVPN/ConfigForge-V2Ray/main/...`
  - Coverage: Pre-tested nodes per country
  - Quality: Higher reliability (pre-filtered)

**Acceptance Criteria**:
- ✅ Fetch all 3 sources in parallel (timeout: 30s each)
- ✅ Parse 100+ nodes without errors
- ✅ Deduplicate across sources
- ✅ Handle source failures gracefully (fallback to others)
- ✅ Cache fetched nodes to SQLite with 1-hour TTL

---

#### FR-1.2: Parse Tunnel Configurations
**Requirement**: Rotaze shall parse tunnel URIs from multiple protocol formats.

**Supported Protocols** (in priority order):
1. **VLESS** (primary)
   - Fields: UUID, server, port, TLS (sni), transport (WS/gRPC)
   - Example: `vless://uuid@server:443?type=ws&security=tls&sni=domain`

2. **Shadowsocks** (secondary)
   - Fields: Method, password, server, port
   - Example: `ss://method:password@server:port`

3. **Trojan** (tertiary)
   - Fields: Password, server, port, TLS
   - Example: `trojan://password@server:443?security=tls`

4. **VMess** (fallback)
   - Fields: UUID, server, port, alter_id
   - Example: `vmess://base64(json)`

5. **Hysteria2** (optional)
   - Fields: Password, server, port, TLS
   - Example: `hy2://password@server:443`

**Parsing Requirements**:
- ✅ Extract all relevant fields
- ✅ Handle base64-encoded components
- ✅ Skip invalid URIs (log warning, continue)
- ✅ Extract country code from URI fragment (#VN, #US, etc)
- ✅ Normalize field formats (e.g., "chacha20-poly1305" → standard method)

**Acceptance Criteria**:
- ✅ Parse 100 URIs without crashes
- ✅ Extract correct server/port/protocol
- ✅ Handle edge cases (missing fields, malformed)
- ✅ >95% successful parse rate

---

#### FR-1.3: Filter & Deduplicate Nodes
**Requirement**: Rotaze shall filter and deduplicate nodes for quality.

**Filtering Logic**:
- Remove duplicate nodes: key = `(protocol, server, port)`
- Filter by country: Match country code in fragment (#VN, #US, etc)
- Support "all" country: Include all nodes regardless of country
- Special handling for country codes:
  - VN matches: "VN", "越南", "🇻🇳" (Vietnamese variations)
  - US matches: "US", "USA", "United States"
  - Others: Exact match or common abbreviations

**Acceptance Criteria**:
- ✅ Remove 100% of duplicates
- ✅ Filter by country with >90% accuracy
- ✅ Support "all" mode (no filtering)
- ✅ Maintain node quality (server, port, protocol valid)

---

#### FR-1.4: Cache Nodes Locally
**Requirement**: Rotaze shall cache fetched nodes in SQLite for quick access.

**Schema**:
```sql
nodes(
  id INTEGER PRIMARY KEY,
  server TEXT,
  port INTEGER,
  protocol TEXT,
  country TEXT,
  uuid TEXT (for VLESS),
  password TEXT (for SS/Trojan),
  tls_enabled BOOLEAN,
  tls_sni TEXT,
  transport_type TEXT (ws/grpc),
  created_at TIMESTAMP,
  fetched_at TIMESTAMP,
  ttl_expires_at TIMESTAMP
)

node_stats(
  node_id INTEGER PRIMARY KEY,
  latency_ms INTEGER,
  success_rate REAL (0.0-1.0),
  consecutive_failures INTEGER,
  last_check TIMESTAMP,
  total_checks INTEGER,
  total_failures INTEGER
)
```

**Cache Management**:
- Automatic refresh: Every 1 hour (TTL expiry)
- Manual refresh: `rotaze pool refresh`
- Keep latest 200+ nodes (LRU if storage limited)
- Persist across restarts (recovery)

**Acceptance Criteria**:
- ✅ Store 100+ nodes efficiently
- ✅ Query by country/protocol in <100ms
- ✅ Automatic TTL cleanup
- ✅ Handle DB errors gracefully

---

### 4.2 sing-box Integration

#### FR-2.1: Embed sing-box Library
**Requirement**: Rotaze shall embed the sing-box library (no external binary).

**Implementation**:
- Import: `github.com/sagernet/sing-box` v1.13+
- Compile-in: All protocol handlers (VLESS, Shadowsocks, etc)
- Lifecycle: Create, Start, PostStart, Stop, Close
- Concurrency: Safe goroutine management, context timeouts

**Acceptance Criteria**:
- ✅ Single binary with no external dependencies
- ✅ Startup time: <500ms from binary to listening
- ✅ No errors on initialization
- ✅ Graceful shutdown on signal

---

#### FR-2.2: Expose SOCKS5 Proxy Locally
**Requirement**: Rotaze shall listen on local SOCKS5 on `127.0.0.1:1080`.

**SOCKS5 Inbound Configuration**:
- Listen address: `127.0.0.1` (hardcoded for security)
- Listen port: `1080` (configurable via config/flag)
- Protocol: SOCKS5 (SOCKS4/4a via fallback)
- No authentication required (local only)
- Sniff enabled: True (detect protocol)

**Acceptance Criteria**:
- ✅ Port 1080 accessible after startup
- ✅ Accept SOCKS5 connections
- ✅ Route traffic correctly to outbound
- ✅ Handle concurrent connections (50+)
- ✅ Proper error responses (SOCKS5 compliant)

---

#### FR-2.3: Dynamic Outbound Selection
**Requirement**: Rotaze shall dynamically switch tunnel (outbound) without restart.

**Outbound Switching**:
- Build config with single primary outbound + direct fallback
- Switch algorithm: Pause inbound checks, update outbound, verify, resume
- Verification: Test connectivity before full switch
- Rollback: Keep previous outbound available if switch fails

**Acceptance Criteria**:
- ✅ Switch nodes without SOCKS5 listener restart
- ✅ In-flight connections handled gracefully
- ✅ New connections route through new node
- ✅ Switch time: <2s, no connection drops

---

### 4.3 URL Monitoring & Rotation

#### FR-3.1: Monitor Target URL
**Requirement**: Rotaze shall periodically check if target URL is accessible.

**Check Behavior**:
- HTTP GET request via SOCKS5 proxy
- Check status code: 200-399 = OK, others = FAIL
- Timeout: 15 seconds per request
- Interval: 30 seconds (configurable)
- Retry on failure: Yes (see FR-3.2)

**Exit IP Verification** (optional, if `--check-ip`):
- Check: `GET https://ipinfo.io/json` via SOCKS5
- Extract: Country code from response
- Compare: Against configured country (VN, US, etc)
- Fail: If country mismatch (even if HTTP 200)

**Acceptance Criteria**:
- ✅ HTTP check succeeds for accessible sites
- ✅ Timeout detected correctly (>15s)
- ✅ Status code checked accurately
- ✅ Exit IP verified correctly
- ✅ Logs show check result + reason for failure

---

#### FR-3.2: Retry Logic with Exponential Backoff
**Requirement**: Rotaze shall retry failed checks before rotating.

**Retry Sequence**:
```
Check 1 FAIL
  ↓ Wait 1 second
Check 2 FAIL
  ↓ Wait 2 seconds
Check 3 FAIL
  ↓ Wait 4 seconds
Check 4 FAIL
  ↓ Rotate to next node
```

**Configuration**:
- `max_retries`: 3 (default)
- `initial_backoff`: 1 second
- `max_backoff`: 10 seconds
- Backoff formula: `min(max_backoff, initial_backoff * 2^attempt)`

**Acceptance Criteria**:
- ✅ Retry exactly `max_retries` times
- ✅ Backoff follows exponential formula
- ✅ Rotation triggered only after all retries exhausted
- ✅ Temporary hiccups don't cause unnecessary rotation

---

#### FR-3.3: Smart Node Selection
**Requirement**: Rotaze shall prefer reliable nodes and avoid consistently-failing ones.

**Node Scoring Algorithm**:
```
Score = (success_rate * 0.7) + (latency_penalty * 0.3)

Categories:
- "Good": success_rate > 75%
- "Okay": 50-75%
- "Bad": < 50%

Selection:
1. Try nodes from "Good" category (round-robin)
2. Fallback to "Okay" if no "Good" available
3. Last resort: "Bad" or untested
```

**Cooldown Logic**:
- Node fails → Mark with failure timestamp
- Cooldown period: 5 minutes
- Don't retry failed node before cooldown expires
- Periodically (every 2 hours) retry "Bad" nodes

**Acceptance Criteria**:
- ✅ Prefer high-success-rate nodes
- ✅ Avoid repeatedly-failing nodes
- ✅ Recovery from bad nodes after cooldown
- ✅ Fallback gracefully when limited options

---

#### FR-3.4: Automatic Rotation on Failure
**Requirement**: Rotaze shall automatically switch to next node when URL fails.

**Rotation Trigger**:
- Event: All retry attempts exhausted (FR-3.2)
- Action: Select next best node (FR-3.3)
- Execution: Switch outbound (FR-2.3)
- Verification: Check URL on new node (1-2 quick checks)
- Logging: Record reason, old/new node, timestamp

**Rotation History**:
```sql
rotations(
  id INTEGER PRIMARY KEY,
  from_node_id INTEGER,
  to_node_id INTEGER,
  reason TEXT ('failure', 'manual', 'refresh', 'startup'),
  status TEXT ('success', 'failed'),
  timestamp TIMESTAMP
)
```

**Acceptance Criteria**:
- ✅ Detect failure accurately
- ✅ Rotate within 10 seconds of detection
- ✅ New node works immediately after rotation
- ✅ Rotation history logged + queryable

---

### 4.4 CLI Interface

#### FR-4.1: Run Modes
**Requirement**: Rotaze shall support multiple run modes via CLI commands.

**Mode 1: `rotaze run`** (One-shot)
```bash
rotaze run --url https://example.com --country VN --check-ip
```
- Fetch nodes
- Test URL via proxy
- Exit 0 (success) or 1 (failure)
- No persistence, no continuous monitoring

**Mode 2: `rotaze watch`** (Continuous, Foreground)
```bash
rotaze watch --url https://example.com --interval 30
```
- Keep running, foreground (Ctrl+C to stop)
- Check URL every 30 seconds
- Show results in real-time
- Rotate on failure
- Useful for: Debugging, monitoring

**Mode 3: `rotaze daemon`** (Background Service)
```bash
rotaze daemon start --config /etc/rotator/config.toml
rotaze daemon stop
rotaze daemon status
```
- Run in background (systemd-friendly)
- Persist state, logs to file
- Accept remote commands while running
- Useful for: Production deployment

**Mode 4: `rotaze interactive`** (REPL)
```bash
rotaze interactive
rotator> pool list
rotator> use 5
rotator> status
rotator> exit
```
- Interactive shell, manual control
- Commands: list, use, lock, rotate, test-url, etc
- Useful for: Exploration, manual troubleshooting

**Acceptance Criteria**:
- ✅ Each mode works independently
- ✅ Correct exit codes (`run`, `test` modes)
- ✅ Proper signal handling (Ctrl+C)
- ✅ Help text available (`--help`)

---

#### FR-4.2: Pool Management Commands
**Requirement**: Rotaze shall provide commands to list and inspect node pool.

**`rotaze pool list`** - List available nodes
```bash
rotaze pool list
rotaze pool list --country VN
rotaze pool list --protocol vless --sort latency

Output:
ID   Server              Port  Protocol  Country  Latency  Success
─────────────────────────────────────────────────────────────────
1    1.2.3.4            443   VLESS     VN       45ms     95%
2    5.6.7.8            443   VLESS     VN       52ms     92%
3    9.10.11.12         443   SS        VN       38ms     98%  ✓ current
```

**Filter Flags**:
- `--country <CC>`: Filter by country (VN, US, JP, etc)
- `--protocol <proto>`: Filter by protocol (vless, ss, trojan)
- `--sort <field>`: Sort by latency, success, random

**Acceptance Criteria**:
- ✅ List 50+ nodes with formatting
- ✅ Filter and sort work correctly
- ✅ Show current node marker (✓)
- ✅ Show stats (latency, success %)

---

#### FR-4.3: Manual Node Selection
**Requirement**: Rotaze shall allow users to manually select specific tunnel.

**`rotaze use <ID|server>`** - Switch to node
```bash
rotaze use 3                     # Use node ID 3
rotaze use --server 9.10.11.12   # Use by server IP
rotaze use --tag "ss-9.10.11.12-8388"  # Use by tag
rotaze use --random              # Pick random node
rotaze use --best                # Pick best performer
```

**Behavior**:
- Switch to specified node immediately
- Verify connectivity (1-2 quick checks)
- Update current node in DB
- Resume auto-rotation (unless locked)
- Log: "Switched to node X by user request"

**Acceptance Criteria**:
- ✅ Switch to specified node successfully
- ✅ Handle invalid node ID gracefully
- ✅ Update DB state
- ✅ Verify new node works

---

#### FR-4.4: Lock / Unlock Nodes
**Requirement**: Rotaze shall allow locking/unlocking to prevent auto-rotation.

**`rotaze lock on|off|show`** - Prevent auto-rotation
```bash
rotaze lock on         # Lock to current node, disable auto-rotation
rotaze lock off        # Unlock, allow auto-rotation
rotaze lock show       # Show lock status

Output (if locked):
Locked to: vless-1.2.3.4:443 (VN)
Auto-rotation: DISABLED
Last rotated: 2024-08-30 13:45:00
```

**Behavior**:
- Lock on: Prevent auto-rotation, keep current node
- Lock off: Re-enable auto-rotation
- Locked node fails: Manual rotation needed (`rotaze rotate`) or unlock
- Survives restarts (persistent in DB)

**Acceptance Criteria**:
- ✅ Lock/unlock state persisted
- ✅ Locked node prevents auto-rotation
- ✅ Clear logging of lock status
- ✅ Unlock re-enables rotation

---

#### FR-4.5: Status & Diagnostics
**Requirement**: Rotaze shall provide status information and diagnostics.

**`rotaze status`** - Show current state
```bash
rotaze status

Output:
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
```

**`rotaze pool stats`** - Node pool overview
```bash
rotaze pool stats

Output:
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

**Acceptance Criteria**:
- ✅ Show current node + last check time
- ✅ Show pool statistics
- ✅ Show lock status
- ✅ Show recent rotations
- ✅ Pretty formatted output

---

#### FR-4.6: Configuration Management
**Requirement**: Rotaze shall support configuration via TOML files.

**Config File Structure** (`config.toml`):
```toml
[checker]
urls = ["https://toplinks.io/kSDVNrX"]
interval = 30
max_retries = 3
timeout = 15
check_ip = true
country = "VN"

[rotation]
strategy = "smart"
min_success_rate = 0.75
cooldown_period = 300

[proxy]
listen_port = 1080
proxy_type = "socks5"

[cache]
db_path = "~/.rotaze/state.db"
node_ttl = 3600

[logging]
level = "info"
file = "~/.rotaze/rotaze.log"
```

**Commands**:
- `rotaze config generate > config.toml` - Generate default config
- `rotaze config validate -f config.toml` - Validate config
- `rotaze config show` - Show current config
- `rotaze config reload` - Reload config (SIGHUP or command)

**Acceptance Criteria**:
- ✅ Parse TOML correctly
- ✅ CLI flags override config
- ✅ Env vars override config (`ROTAZE_COUNTRY=US`)
- ✅ Validation catches invalid values
- ✅ Reload without restart (SIGHUP)

---

### 4.5 State Persistence & Recovery

#### FR-5.1: Persistent State Storage
**Requirement**: Rotaze shall persist state to SQLite for crash recovery.

**State Tables**:
```sql
-- Current operation state
state(
  key TEXT PRIMARY KEY,
  value TEXT,
  updated_at TIMESTAMP
);
-- Keys: current_node_id, locked_node_id, last_check_time

-- Node performance tracking
node_stats(
  node_id INTEGER PRIMARY KEY,
  latency_ms INTEGER,
  success_rate REAL,
  consecutive_failures INTEGER,
  last_check TIMESTAMP,
  last_failure TIMESTAMP,
  total_checks INTEGER,
  total_failures INTEGER
);

-- Rotation history
rotations(
  id INTEGER PRIMARY KEY,
  from_node_id INTEGER,
  to_node_id INTEGER,
  reason TEXT,
  status TEXT,
  timestamp TIMESTAMP
);
```

**Recovery Behavior**:
- On startup: Load current_node_id from DB
- Resume with last active node (verify it still works)
- If last node dead: Auto-rotate to next
- Preserve rotation history

**Acceptance Criteria**:
- ✅ State persists across crashes
- ✅ Recover current node on restart
- ✅ Stats updated continuously
- ✅ No data loss

---

### 4.6 Logging & Monitoring

#### FR-6.1: Structured Logging
**Requirement**: Rotaze shall use structured logging (JSON/text) to file + stdout.

**Log Levels**:
- `debug`: Verbose diagnostics (parser details, retry attempts)
- `info`: User-facing events (rotations, node switches)
- `warn`: Potential issues (low success rate, source failures)
- `error`: Failures (node dead, config invalid)

**Log Output**:
- File: `~/.rotaze/rotaze.log` (configurable)
- Stdout: Info+ (configurable)
- Rotation: Daily or size-based

**Sample Logs**:
```
2024-08-30T13:45:00Z INFO   rotations.go:42   Rotated to next node  {"node_id": 3, "server": "9.10.11.12", "port": 443, "reason": "failure"}
2024-08-30T13:45:01Z DEBUG  checker.go:78     URL check passed      {"url": "https://example.com", "status_code": 200, "latency_ms": 123}
2024-08-30T13:45:15Z WARN   fetcher.go:156    Source failed         {"source": "Au1rxx", "error": "http 429", "retry_after": 300}
```

**Acceptance Criteria**:
- ✅ Structured (JSON or key=value)
- ✅ File + stdout output
- ✅ Appropriate levels used
- ✅ Log rotation working

---

## 5. Non-Functional Requirements

### 5.1 Performance
- **Startup time**: <500ms from binary to proxy listening
- **SOCKS5 latency**: <50ms overhead per request (should be near-zero)
- **URL check latency**: <15 seconds (includes network + parsing)
- **Node rotation time**: <10 seconds from failure detection to new IP
- **Memory usage**: <100MB idle, <200MB under load
- **CPU usage**: <5% idle, <20% during rotation

### 5.2 Reliability
- **Node availability**: 95%+ of rotation attempts succeed
- **Uptime**: 99%+ availability via rotation (assumes at least 1 working node)
- **Recovery time**: <10 seconds from node failure to new IP
- **Crash recovery**: Restart and resume with last state
- **State corruption**: Graceful handling (rebuild DB if corrupted)

### 5.3 Scalability
- **Concurrent connections**: Handle 50+ simultaneous SOCKS5 connections
- **Node pool size**: 200+ nodes without performance degradation
- **Check frequency**: 30-second intervals sustainable
- **Log volume**: <100MB/week typical usage

### 5.4 Security
- **SOCKS5 binding**: Localhost only (`127.0.0.1`, not `0.0.0.0`)
- **No hardcoded credentials**: All config via files/env
- **Tunnel validation**: Verify tunnel works before switching
- **State file permissions**: `600` (user only)
- **Log sanitization**: No credentials in logs

### 5.5 Compatibility
- **OS**: Linux (primary), macOS (secondary), Windows (nice-to-have)
- **Go version**: 1.22+
- **Architecture**: amd64, arm64, armv7
- **Dependencies**: Minimal external (see go.mod)

### 5.6 Maintainability
- **Code structure**: Clear separation of concerns (fetcher, rotator, checker, etc)
- **Testing**: >80% unit test coverage for core packages
- **Documentation**: README, examples, API docs
- **Error messages**: Clear, actionable (help user fix issues)
- **Comments**: Explain "why", not "what"

---

## 6. User Stories & Scenarios

### Scenario 1: Developer Testing (David)
**Goal**: Test API with multiple IPs simultaneously

```bash
# Start Rotaze
./rotaze watch --url https://api.example.com/health

# In another terminal, run tests
for i in {1..10}; do
  curl --proxy socks5://127.0.0.1:1080 https://api.example.com/test
done

# Expected: 10 requests from different IPs (via rotation if needed)
```

### Scenario 2: Production Deployment (Sarah)
**Goal**: Continuous access via rotating proxy

```bash
# Install systemd service
sudo cp rotaze.service /etc/systemd/system/
sudo systemctl enable rotaze
sudo systemctl start rotaze

# Apps connect to 127.0.0.1:1080 automatically
# Rotaze maintains uptime via auto-rotation
# Logs: /var/log/rotaze.log
```

### Scenario 3: Manual Control (Alex)
**Goal**: Explore nodes and pick specific one

```bash
./rotaze interactive

rotator> pool list --country VN --sort latency
ID   Server            Latency  Success
1    1.2.3.4           38ms     98%
2    5.6.7.8           45ms     95%

rotator> use 1
✓ Switched to node 1

rotator> lock on
✓ Locked to node 1 (auto-rotation disabled)

rotator> test-url https://example.com
✓ OK (200)

rotator> exit
```

---

## 7. Acceptance Criteria (MVP Definition)

### Core Features Complete
- [ ] Fetch nodes from 3 sources (Au1rxx, SSAgg, ConfigForge)
- [ ] Parse VLESS, SS, Trojan, VMess, Hy2 protocols
- [ ] Embed sing-box (no binary dependency)
- [ ] Expose SOCKS5 on 127.0.0.1:1080
- [ ] Monitor URL + auto-rotate on failure
- [ ] Retry logic with exponential backoff
- [ ] Smart node selection (prefer good performers)
- [ ] Manual node picking (pool list, use command)
- [ ] Lock/unlock nodes (prevent auto-rotation)
- [ ] Multiple run modes (run, watch, daemon, interactive)
- [ ] SQLite state persistence
- [ ] Configuration via TOML
- [ ] Structured logging

### Quality Standards Met
- [ ] >80% unit test coverage (core packages)
- [ ] All critical errors handled gracefully
- [ ] No goroutine leaks or race conditions
- [ ] Cross-compile to Linux amd64 + arm64
- [ ] Systemd service file working
- [ ] Documentation complete (README, examples, config)
- [ ] Manual testing checklist passed
- [ ] Performance targets met (<10s rotation, <100MB memory)

### User Workflows Tested
- [ ] Developer: `rotaze watch` + app traffic via proxy
- [ ] DevOps: `rotaze daemon` + systemd service
- [ ] Power user: `rotaze interactive` + manual control
- [ ] One-shot: `rotaze run --url https://test.com`

---

## 8. Out of Scope (Phase 2+)

### Features NOT in MVP
- Web dashboard / UI
- Prometheus metrics endpoint
- Advanced routing rules (based on URL pattern, country, etc)
- Multi-URL monitoring (round-robin checking)
- Node quality scoring (reserved for phase 2)
- Custom node sources (whitelisting user lists)
- API server (clients connect via SOCKS5, not REST)
- Windows support (Linux primary)
- Integration with external tools (Kubernetes, Docker Swarm)

### Why Deferred
- Phase 1 focus: Core stability + usability
- Phase 2 can add: Advanced monitoring + customization
- Phase 3 can add: Ecosystem integration

---

## 9. Success Metrics & KPIs

### Functional Metrics
- **Node availability**: Track % of nodes working at any time (target: 80%+)
- **Rotation accuracy**: % rotations that result in accessible URL (target: 95%+)
- **TTL to new IP**: Time from failure to working new IP (target: <10s)
- **Pool diversity**: # unique countries in pool (target: 20+)

### Performance Metrics
- **Startup latency**: Time to SOCKS5 listening (target: <500ms)
- **SOCKS5 overhead**: Proxy latency vs direct (target: <50ms)
- **Memory footprint**: RSS at idle (target: <100MB)
- **CPU usage**: Idle + rotating (target: <5% idle, <20% rotating)

### Reliability Metrics
- **Uptime**: % time URL stays accessible via rotation (target: 95%+)
- **Mean time to recovery (MTTR)**: Avg time to recover from node failure (target: <10s)
- **Crash recovery**: Successful restart + resume last state (target: 100%)
- **Log errors**: Track error frequency, categorize by type

---

## 10. Glossary

| Term | Definition |
|------|-----------|
| **Node** | A tunnel server configuration (server, port, protocol, credentials) |
| **Outbound** | sing-box term for proxy destination (node) |
| **SOCKS5** | Proxy protocol exposed by Rotaze locally |
| **Rotation** | Switching from one node to another |
| **Failover** | Automatic rotation triggered by URL failure |
| **Cooldown** | Waiting period before retrying a failed node |
| **TTL** | Time-to-live for cached nodes before refresh |
| **Inbound** | sing-box term for local listener (SOCKS5) |
| **sing-box** | Embedded proxy library (VLESS, SS, Trojan support) |
| **Au1rxx** | Free VPN subscription source #1 |
| **SSAgg** | Free VPN subscription source #2 (aggregator) |
| **ConfigForge** | Free VPN subscription source #3 (per-country) |

---

## 11. Document Control

| Section | Version | Updated | Notes |
|---------|---------|---------|-------|
| Executive Summary | 1.0 | 2024-08-30 | Initial draft |
| Functional Requirements | 1.0 | 2024-08-30 | Complete MVP scope |
| Non-Functional Requirements | 1.0 | 2024-08-30 | Performance, reliability targets |
| Acceptance Criteria | 1.0 | 2024-08-30 | MVP definition |

---

**Document Version**: 1.0  
**Last Updated**: 2026-08-30 
**Status**: Approved for Development  
**Next Review**: Post-Phase-1 Completion

