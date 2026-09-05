# ARCHITECTURE.md - Rotaze Technical Design

**Project**: Rotaze (IP Rotator with Embedded sing-box)  
**Version**: 1.0  
**Date**: 2026-08-30

---

## 1. System Architecture Overview

### High-Level Diagram
```
┌─────────────────────────────────────────────────────────────┐
│                    User Applications                         │
│              (Browser, curl, Python, etc)                    │
└────────────────────┬────────────────────────────────────────┘
                     │
                     │ SOCKS5 127.0.0.1:1080
                     ▼
         ┌──────────────────────────────┐
         │   Rotaze (Go binary)         │
         │                              │
         │  ┌────────────────────────┐  │
         │  │  CLI/Daemon Entry      │  │
         │  │  (Cobra, systemd)      │  │
         │  └───────────┬────────────┘  │
         │              │               │
         │  ┌───────────▼────────────┐  │
         │  │  sing-box (embedded)   │  │
         │  │  ┌──────────────────┐  │  │
         │  │  │ SOCKS5 Inbound   │  │  │ ◄──── Proxy listener
         │  │  │ 127.0.0.1:1080   │  │  │
         │  │  └──────────────────┘  │  │
         │  │  ┌──────────────────┐  │  │
         │  │  │ Outbound Router  │  │  │ ◄──── Routes to tunnel
         │  │  │ (VLESS/SS/etc)   │  │  │
         │  │  └──────────────────┘  │  │
         │  └───────────┬────────────┘  │
         │              │               │
         │  ┌───────────▼────────────┐  │
         │  │  Checker (URL monitor) │  │
         │  │  + Retry Logic         │  │
         │  └───────────┬────────────┘  │
         │              │               │
         │  ┌───────────▼────────────┐  │
         │  │  Rotator (switcher)    │  │
         │  │  + Node scoring        │  │
         │  └───────────┬────────────┘  │
         │              │               │
         │  ┌───────────▼────────────┐  │
         │  │  Cache (SQLite)        │  │
         │  │  + State persistence   │  │
         │  └────────────────────────┘  │
         └──────────────────────────────┘
                     │
        ┌────────────┴────────────┐
        ▼                         ▼
   ┌─────────────┐          ┌──────────────┐
   │  Internet   │          │ ipinfo.io    │
   │(via tunnel) │          │(verify exit) │
   └─────────────┘          └──────────────┘
```

### Component Interaction Flow

```
1. User runs: rotaze watch --url https://example.com

2. CLI Initialization
   └─ Parse flags/config
   └─ Load configuration (TOML)
   └─ Initialize logger (Zap)
   └─ Open SQLite cache
   └─ Initialize sing-box Box

3. Fetcher (Bootstrap)
   └─ Fetch nodes from 3 sources (parallel)
   └─ Parse URIs (VLESS, SS, Trojan, etc)
   └─ Deduplicate, filter by country
   └─ Save to SQLite cache

4. Rotator (First Node)
   └─ Load "current node" from cache (or pick best)
   └─ Build sing-box config with SOCKS5 inbound + outbound
   └─ Start Box (listen on 127.0.0.1:1080)
   └─ Resume listening for SOCKS5 connections

5. Checker Loop (Every 30 seconds)
   └─ Make HTTP GET to URL via SOCKS5 proxy
   └─ Check status code (200-399 = OK)
   └─ If OK: Log success, continue loop
   └─ If FAIL: Enter retry logic (FR-3.2)
   
6. Retry Logic
   └─ Attempt 1: Wait 1s, check again
   └─ Attempt 2: Wait 2s, check again
   └─ Attempt 3: Wait 4s, check again
   └─ All failed: Trigger rotation

7. Rotation
   └─ Select next good node (Rotator.SelectNextNode)
   └─ Build new config, switch outbound
   └─ Quick verification (1-2 checks)
   └─ Log rotation reason + success
   └─ Resume checker loop

8. Continuous Operation
   └─ Background health checks on node pool
   └─ Update node stats (latency, success rate)
   └─ Periodic refresh of node list (1-hour TTL)
   └─ Handle signals (SIGTERM, SIGHUP)
   └─ Graceful shutdown
```

---

## 2. Module Breakdown & Dependencies

### 2.1 `cmd/rotaze/main.go`
**Purpose**: CLI entry point, orchestration  
**Responsibilities**:
- Parse arguments (Cobra)
- Load configuration (Viper)
- Initialize logger (Zap)
- Open SQLite cache
- Dispatch to appropriate command handler

**Dependencies**:
- `github.com/spf13/cobra` - CLI framework
- `internal/cli` - Command handlers
- `internal/config` - Config loading
- `internal/logger` - Logging setup
- `internal/cache` - DB initialization

**Key Functions**:
- `main()` - Entry point
- `setupCobraRoot()` - Cobra command tree
- `initGlobals()` - Logger, config, cache setup

---

### 2.2 `internal/fetcher/` Package
**Purpose**: Node fetching from 3 sources, URI parsing  
**Responsibilities**:
- Fetch from Au1rxx, SSAgg, ConfigForge (concurrent)
- Parse URI schemes (VLESS, SS, Trojan, VMess, Hy2)
- Handle errors gracefully (404, 429, timeout)
- Deduplicate nodes
- Filter by country

**Key Components**:

#### `sources.go`
- `fetchAu1rxx(ctx, country)` - Fetch Au1rxx subscription
- `fetchSSAgg(ctx, country)` - Fetch merged base64
- `fetchConfigForge(ctx, country)` - Fetch pre-tested list
- Concurrent fetching with context timeout
- Fallback logic (if source fails, try others)

#### `parser.go`
- `parseVLESS(uri string) (*Node, error)` - VLESS parser
- `parseShadowsocks(uri string) (*Node, error)` - SS parser
- `parseTrojan(uri string) (*Node, error)` - Trojan parser
- `parseVMess(uri string) (*Node, error)` - VMess parser
- `parseHy2(uri string) (*Node, error)` - Hysteria2 parser
- Common helpers: Base64 decode, URL parsing, TLS extraction

#### `pool.go`
- `FetchNodes(ctx, country)` - Orchestrate: fetch all sources, parse, deduplicate
- `DeduplicateNodes(nodes []*Node)` - Remove duplicates (key: server+port+protocol)
- `FilterByCountry(nodes, country)` - Country filtering
- `SaveToCache(cache db, nodes)` - Persist to SQLite

**Dependencies**:
- `encoding/json`, `encoding/base64` - Parsing
- `net/url` - URL handling
- `regexp` - Country matching
- `context` - Timeouts
- `internal/cache` - Save nodes

---

### 2.3 `internal/rotator/` Package
**Purpose**: sing-box lifecycle, outbound switching  
**Responsibilities**:
- Initialize sing-box Box instance
- Build dynamic configs (SOCKS5 inbound + outbound)
- Switch outbounds without restart
- Handle lifecycle (Start, Close)
- Error recovery

**Key Components**:

#### `box.go`
- `NewRotator(ctx)` - Create rotator instance
- `(r *Rotator) Start()` - Start sing-box
- `(r *Rotator) Close()` - Shutdown gracefully
- `(r *Rotator) SwitchOutbound(ctx, nodTag)` - Change outbound
- Manages: sing-box.Box instance, lifecycle events
- Error handling: Config invalid, port in use, etc

**Concurrency model** (actual implementation in `rotator.go`):
- `Start()`/`SwitchOutbound()` spawn a single monitor goroutine that owns `cmd.Wait()`
- The process exit result is published on `waitCh` (buffered channel)
- `Close()`/`SwitchOutbound()` drain `waitCh` (never call `cmd.Wait()` themselves) — avoids the data race of concurrent `Wait()` calls
- On timeout, `Process.Kill()` is sent, then `waitCh` is drained again to reap the killed process

#### `config.go`
- `BuildConfig(node *Node, port int)` - Generate sing-box config JSON
- SOCKS5 inbound specification
- Outbound routing for current node
- DNS resolution settings
- Route rules (direct fallback)

**Dependencies**:
- `github.com/sagernet/sing-box` - sing-box library
- `github.com/sagernet/sing-box/option` - Config structs
- `encoding/json` - Config serialization

---

### 2.4 `internal/checker/` Package
**Purpose**: URL monitoring, exit IP verification  
**Responsibilities**:
- Check URL via SOCKS5 proxy
- Verify exit IP country (optional)
- Retry logic with exponential backoff
- Track check results (success/failure/latency)
- Trigger rotation on exhausted retries

**Key Components**:

#### `http.go`
- `CheckURLViaSOCKS5(ctx, url, socks5Addr)` - HTTP check
- Use curl subprocess (if available) or pure Python backend
- Extract status code, measure latency
- Return: status_code, error, latency_ms
- Timeout: 15 seconds per request

#### `ipinfo.go`
- `VerifyExitIP(ctx, country)` - Check exit IP country
- Hit: `https://ipinfo.io/json` via SOCKS5
- Extract: `country` field
- Compare: Against configured country
- Return: (country, ip, error)

#### `checker.go`
- `(c *Checker) CheckLoop(ctx, interval)` - Main loop
- Periodic checks (default: 30s interval)
- Call `CheckURLViaSOCKS5()`, log result
- On failure: Call `RetryWithBackoff()`
- On exhausted retries: Return error (trigger rotation)

#### `retry.go`
- `RetryWithBackoff(ctx, config)` - Exponential backoff retry
- Attempts: 1s, 2s, 4s, 10s (configurable)
- Stop condition: Max retries exhausted
- Return: Last error, all failed

**Dependencies**:
- `context` - Timeouts
- `net/http`, `http.Client` - HTTP requests (Python fallback)
- `subprocess` or `net.Dial` - SOCKS5 connection
- `math` - Exponential calculation
- `time` - Backoff delays

---

### 2.5 `internal/cache/` Package
**Purpose**: SQLite state persistence, node + stats storage  
**Responsibilities**:
- Database initialization & migrations
- Node CRUD (Create, Read, Update, Delete)
- Node stats tracking (latency, success rate)
- Current node state (recovery on crash)
- Lock state (persist lock status)
- Rotation history (audit trail)

**Key Components**:

#### `models.go`
- `type Node struct` - Tunnel config (server, port, protocol, etc)
- `type NodeStats struct` - Performance metrics
- `type State struct` - Current state (current_node_id, locked_node_id)
- `type Rotation struct` - History entry

#### `cache.go`
- `NewCache(dbPath string)` - Open SQLite connection
- `(c *Cache) Init()` - Create schema, run migrations
- Database connection pooling, connection management

#### `node_store.go`
- `(c *Cache) SaveNodes(nodes []*Node)` - Batch insert nodes
- `(c *Cache) GetNodes()` - Fetch all nodes
- `(c *Cache) GetNodesByCountry(country)` - Filter by country
- `(c *Cache) GetNodesByProtocol(protocol)` - Filter by protocol
- `(c *Cache) GetGoodNodes(minSuccessRate float64)` - Performance-based
- `(c *Cache) GetNodeByID(id int)` - Single node lookup
- `(c *Cache) DeleteOldNodes(ttl time.Duration)` - TTL cleanup

#### `state_store.go`
- `(c *Cache) SetCurrentNode(nodeID int)` - Persist current
- `(c *Cache) GetCurrentNode()` - Recover current
- `(c *Cache) LockNode(nodeID int)` - Persist lock
- `(c *Cache) UnlockNode()` - Clear lock
- `(c *Cache) IsNodeLocked()` - Check lock status

#### `stats_store.go`
- `(c *Cache) UpdateNodeStats(nodeID, latencyMS int, success bool)` - Record check
- `(c *Cache) IncrementConsecutiveFailures(nodeID int)` - Cooldown tracking
- `(c *Cache) ResetConsecutiveFailures(nodeID int)` - After success
- `(c *Cache) GetNodeStats(nodeID int)` - Fetch stats

#### `rotation_store.go`
- `(c *Cache) RecordRotation(from, to int, reason, status)` - History entry
- `(c *Cache) GetRecentRotations(limit int)` - Recent history
- `(c *Cache) GetRotationStats()` - Aggregate stats

**Dependencies**:
- `gorm.io/gorm` - ORM
- `gorm.io/driver/sqlite` - SQLite driver
- `time`, `database/sql` - DB operations

**Schema** (simplified):
```sql
nodes (id, server, port, protocol, country, uuid, tls_enabled, ...)
node_stats (node_id, latency_ms, success_rate, consecutive_failures, ...)
state (key TEXT PRIMARY KEY, value TEXT) -- current_node_id, locked_node_id
rotations (id, from_node_id, to_node_id, reason, status, timestamp)
```

---

### 2.6 `internal/config/` Package
**Purpose**: Configuration loading & management  
**Responsibilities**:
- Parse TOML config files
- Env var overrides
- CLI flag overrides
- Validation (ensure valid values)
- Reload support (SIGHUP)

**Key Components**:

#### `config.go`
- `type Config struct` - Full configuration
  ```go
  type Config struct {
      Checker struct {
          URLs         []string
          Interval     int       // seconds
          MaxRetries   int
          InitialBackoff int
          Timeout      int
          CheckIP      bool
          Country      string
      }
      Rotation struct {
          Strategy         string  // "smart", "random"
          MinSuccessRate   float64
          CooldownPeriod   int
      }
      // ... more sections
  }
  ```
- `LoadConfig(path string) (*Config, error)` - Parse TOML + env overrides
- `ValidateConfig(c *Config) error` - Validation (country codes, ports, etc)
- `ReloadConfig(path string)` - SIGHUP handler

**Dependencies**:
- `github.com/spf13/viper` - Config parsing + overrides
- `gopkg.in/yaml.v3` or `BurntSushi/toml` - TOML parsing

---

### 2.7 `internal/cli/` Package
**Purpose**: Cobra commands, user interface  
**Responsibilities**:
- Define CLI commands (run, watch, daemon, pool, use, lock, status, interactive)
- Parse flags for each command
- Orchestrate components (fetcher, rotator, checker, cache)
- Format output (tables, colors, JSON)
- Error handling & user guidance

**Key Command Files**:

#### `root.go`
- Setup Cobra root command
- Global flags: `--config`, `--verbose`, `--log-file`
- Command tree initialization

#### `run.go` - `rotaze run`
- One-shot URL check
- Flags: `--url`, `--country`, `--check-ip`
- Flow: Fetch → Parse → Start box → Check URL → Exit (0 or 1)

#### `watch.go` - `rotaze watch`
- Continuous monitoring, foreground
- Flags: `--url`, `--interval`, `--timeout`, `--country`, `--check-ip`
- Flow: Fetch → Loop check → Rotate on fail → Ctrl+C to stop

#### `daemon.go` - `rotaze daemon`
- Background service management
- Commands: `start`, `stop`, `status`, `restart`
- Flags: `--config`, `--log-file`
- Use systemd or daemonize (go-daemon lib)

#### `pool.go` - `rotaze pool list`
- Show available nodes
- Flags: `--country`, `--protocol`, `--sort`
- Output: Formatted table (ID, server, port, protocol, country, latency, success)
- Commands: `list`, `refresh`, `stats`

#### `use.go` - `rotaze use`
- Manual node selection
- Arguments: `<ID>` or flags: `--server`, `--tag`, `--random`, `--best`
- Verify + switch via Rotator
- Log: "Switched to node X"

#### `lock.go` - `rotaze lock`
- Lock/unlock to current node
- Commands: `on`, `off`, `show`
- Prevent auto-rotation when locked

#### `status.go` - `rotaze status`
- Show current state
- Display: Current node, last check, pool stats, recent rotations

#### `interactive.go` - `rotaze interactive`
- REPL (Read-Eval-Print Loop)
- Prompt: `rotator> `
- Commands: `list`, `use`, `lock`, `status`, `test-url`, `rotate`, `exit`
- In-loop access to all operations

**Dependencies**:
- `github.com/spf13/cobra` - CLI framework
- `github.com/olekukonko/tablewriter` - Table formatting
- `github.com/fatih/color` - Colors
- All above packages (fetcher, rotator, checker, cache, config)

---

### 2.8 `internal/logger/` Package
**Purpose**: Structured logging  
**Responsibilities**:
- Initialize Zap logger
- File + stdout output
- Configurable levels (debug, info, warn, error)
- Log rotation

**Key Components**:

#### `logger.go`
- `InitLogger(config LogConfig)` - Setup Zap
- Output destinations: Stdout (info+), File (all levels)
- Structured logging (JSON or text)
- Log rotation: Daily or size-based

**Dependencies**:
- `go.uber.org/zap` - Structured logging
- `os`, `path/filepath` - File operations

---

### 2.9 `pkg/types/` Package
**Purpose**: Shared data types  
**Responsibilities**:
- Common structs: Node, NodeStats, Config, etc
- Used across packages
- Type safety, consistency

**Key Types**:
```go
type Node struct {
    ID            int
    Server        string
    Port          int
    Protocol      string  // vless, ss, trojan, vmess, hy2
    Country       string
    UUID          string  // VLESS
    Password      string  // SS, Trojan, Hy2
    TLSEnabled    bool
    TLSSNI        string
    TransportType string  // ws, grpc, etc
    Tag           string  // sing-box tag
}

type NodeStats struct {
    NodeID               int
    LatencyMS            int64
    SuccessRate          float64  // 0.0-1.0
    ConsecutiveFailures  int
    LastCheck            time.Time
    LastFailure          time.Time
}

type State struct {
    CurrentNodeID int
    LockedNodeID  int  // 0 = unlocked
}
```

---

## 3. Data Flow Diagrams

### Flow 1: Bootstrap (First Run)
```
User runs: rotaze run --url https://example.com --country VN
                │
                ▼
        CLI parses flags
                │
                ▼
        Load config (TOML)
                │
                ▼
        Initialize logger, DB, sing-box
                │
                ▼
        ┌───────┴────────┬────────────┐
        ▼                ▼            ▼
    Fetch Au1rxx   Fetch SSAgg   Fetch ConfigForge
        │                │            │
        └────────────┬───┴────────────┘
                     ▼
            Parse all URIs
                     │
                     ▼
            Deduplicate nodes
                     │
                     ▼
            Filter by country (VN)
                     │
                     ▼
            Save to SQLite (~50+ nodes)
                     │
                     ▼
            Pick best node (or current from cache)
                     │
                     ▼
            Build sing-box config
                     │
                     ▼
            Start Box (listen on 127.0.0.1:1080)
                     │
                     ▼
            Check URL via SOCKS5
                     │
                 ┌───┴───┐
            SUCCESS   FAILURE
                │        │
                ▼        ▼
            Log OK    Retry logic (backoff)
                │        │
                │     ┌──┴──┐
                │  PASS  FAIL
                │     │     │
                │     ▼     ▼
                │   Log OK Rotate to next
                │           │
                │           └─→ Check on new node
                │
                ▼
            Exit code 0 (success)
```

### Flow 2: Continuous Monitoring (watch mode)
```
Initial setup (same as above)
                │
                ▼
        Loop: Check URL every 30s
                │
        ┌───────┴────────┐
        ▼                ▼
    Status OK        Status FAIL
        │                │
        ▼                ▼
    Log success     Retry (1s backoff)
        │                │
        │            ┌───┴───┐
        │        PASS   FAIL
        │            │     │
        │            │     └─→ Retry (2s backoff)
        │            │             │
        │            │         ┌───┴───┐
        │            │     PASS   FAIL
        │            │         │     │
        │            │         │     └─→ Retry (4s backoff)
        │            │         │             │
        │            │         │         ┌───┴───┐
        │            │         │     PASS   FAIL
        │            │         │         │     │
        │            │         │         │     └─→ All retries exhausted
        │            │         │         │             │
        │            │         │         │             ▼
        │            │         └────┬────┴──→ Rotation triggered
        │            │              │             │
        │            └──────────────┴──→ Select next good node
        │                                 │
        │                    ┌────────────┘
        │                    ▼
        │            Switch outbound
        │                    │
        │         ┌──────────┘
        │         ▼
        │    Verify on new node
        │         │
        │     ┌───┴───┐
        │  PASS   FAIL
        │     │     │
        │     ▼     ▼
        │  Log OK Try another (cooldown)
        │     │
        └─────┴─→ Resume loop (back to "Loop: Check URL")
                │
                └─→ On signal (Ctrl+C): Shutdown gracefully
```

### Flow 3: Manual Node Selection
```
User runs: rotaze interactive
                │
                ▼
        REPL prompt: rotator>
                │
        User inputs: pool list
                │
                ▼
        Fetch nodes from cache (DB)
                │
                ▼
        Format & display table
                │
        Prompt: rotator>
                │
        User inputs: use 3
                │
                ▼
        Lookup node ID 3
                │
                ▼
        Build config, switch outbound
                │
                ▼
        Verify connectivity
                │
                ▼
        Log: "Switched to node 3"
                │
                ▼
        Update state in DB (current_node_id = 3)
                │
                ▼
        Resume: rotator>
```

---

## 4. Technology Stack

### Language & Runtime
- **Go**: 1.22+
- **Target OS**: Linux (primary), macOS (secondary)
- **Architecture**: amd64, arm64, armv7

### Core Dependencies
```go
// sing-box
github.com/sagernet/sing-box v1.13.3
github.com/sagernet/sing v0.5.1

// CLI
github.com/spf13/cobra v1.7.0
github.com/spf13/viper v1.17.0

// Database
gorm.io/gorm v1.25.5
gorm.io/driver/sqlite v1.5.4

// Logging
go.uber.org/zap v1.26.0

// HTTP/Network
github.com/go-resty/resty/v2 v2.10.0

// Utils
github.com/google/uuid v1.5.0

// Formatting
github.com/olekukonko/tablewriter v0.0.5
github.com/fatih/color v1.16.0
```

### External Tools
- **curl** (optional): For SOCKS5 checks (fallback: pure Go HTTP)
- **Python** (optional): Alternative backend for SOCKS5 checks
- **systemd** (Linux): Service management

---

## 5. Error Handling Strategy

### Error Categories

#### Network Errors
- **Source fetch failure** (404, 429, timeout)
  - Action: Log warning, skip source, use alternatives
  - Retry: Backoff before retry
  
- **URL check timeout** (>15s)
  - Action: Treat as FAIL, retry logic kicks in
  - Retry: Yes (exponential backoff)

- **SOCKS5 connection failure**
  - Action: Rotate to next node
  - Retry: No (immediate rotation)

#### Configuration Errors
- **Invalid country code**
  - Action: Reject config, error message
  - Recovery: User must fix config

- **Invalid port**
  - Action: Reject, suggest valid range
  - Recovery: User must fix config

- **Unknown protocol in node**
  - Action: Skip node, log warning
  - Recovery: Try next node

#### System Errors
- **SQLite corruption**
  - Action: Backup old DB, reinitialize schema
  - Recovery: Fetch fresh nodes, resume

- **Port already in use**
  - Action: Exit with error
  - Recovery: User must stop conflicting process or change port

- **sing-box startup failure**
  - Action: Log error details, exit
  - Recovery: Check sing-box build, file permissions

### Error Propagation
- Fetch errors: Non-fatal (fallback to other sources)
- Check errors: Non-fatal (retry logic)
- Rotation errors: Fatal (requires manual intervention)
- Config errors: Fatal (must fix config)

---

## 6. Concurrency Model

### Goroutine Usage

#### Main Goroutine
- CLI parsing, orchestration
- Signal handling (SIGTERM, SIGHUP, SIGINT)
- REPL loop (if interactive mode)

#### Fetcher Goroutines
- 3 concurrent source fetchers (one per source)
- Context timeout: 30 seconds per source
- Channel: Results aggregation

#### Checker Goroutine
- Periodic URL checks
- Ticker: Every N seconds
- Blocking: One check at a time (sequential)

#### Rotator Goroutine
- Triggered on check failure
- Non-blocking: Handled from checker goroutine

#### sing-box Internal Goroutines
- Managed by sing-box Box
- Connection handling, protocol handlers
- No manual goroutine creation needed

### Synchronization Primitives
- **Context**: Timeouts for all I/O operations
- **Channels**: Result aggregation (fetcher)
- **Mutex**: Protected shared state (cache, current node)
- **Atomic**: Flags (shutdown signal)

### Potential Race Conditions & Mitigation
1. **Concurrent node fetch + check**
   - Mitigation: Lock DB access via GORM (thread-safe)

2. **Config reload + active check**
   - Mitigation: Load new config into memory, atomic swap

3. **Multiple CLI instances**
   - Mitigation: File lock on DB (if supported), or warning in docs

---

## 7. Performance Optimization Strategy

### Startup Time (<500ms target)
- Lazy initialization: Defer non-critical ops
- Database: Connection pooling
- sing-box: Pre-built config (JSON), quick parsing

### Memory Usage (<100MB idle target)
- Cache only recent 200 nodes (LRU eviction)
- Efficient data structures (slices vs maps where appropriate)
- sing-box: Minimal goroutines

### Check Latency (<15s per check target)
- Parallel fetcher (3 concurrent sources)
- Timeouts: Enforce 30s per source
- Caching: Use cached nodes if fetch fails

### Node Rotation Time (<10s target)
- Pre-built outbound: Load from cache
- Verify: 1-2 quick checks (parallel)
- Log: Async (non-blocking)

---

## 8. Security Considerations

### Local-Only Binding
- SOCKS5 binds to `127.0.0.1:1080` (not `0.0.0.0`)
- Prevents accidental exposure on network

### No Credential Storage
- Tunnel credentials stored in cache (SQLite)
- File permissions: `600` (user only)
- No plain-text in logs or config

### State File Permissions
- `~/.rotaze/state.db`: `600` (read/write user only)
- `~/.rotaze/rotaze.log`: `644` (readable by user)

### Input Validation
- Country codes: Whitelist known codes
- URLs: Parse via `url.Parse()`, validate scheme
- Ports: Range check (1-65535)
- Node configs: Syntax validation before use

### Signal Handling
- Graceful shutdown on SIGTERM/SIGINT
- Flush logs, close DB, stop sing-box
- No abrupt termination

---

## 9. Deployment Topology

### Development
```
Debian 13 chroot (Codespaces)
└─ rotaze (single binary)
   └─ 127.0.0.1:1080 (SOCKS5)
   └─ ~/.rotaze/state.db (SQLite)
   └─ ~/.rotaze/rotaze.log (logs)
```

### Production (systemd)
```
Linux Server
└─ /usr/local/bin/rotaze (binary)
└─ /etc/rotaze/config.toml (config)
└─ /var/lib/rotaze/ (state)
   └─ state.db
   └─ rotaze.log
└─ systemd unit: rotaze.service
   └─ ExecStart=/usr/local/bin/rotaze daemon start
   └─ Restart=on-failure
   └─ User=rotaze (dedicated)
└─ Apps connect to 127.0.0.1:1080
```

### Multi-Instance (advanced)
```
Machine A                     Machine B
└─ rotaze (port 1080)        └─ rotaze (port 1081)
   └─ Country: VN               └─ Country: US
   └─ Dedicated DB              └─ Dedicated DB

Apps: Round-robin between A:1080 and B:1081
```

---

## 10. Testing Strategy

### Unit Tests
- **Fetcher**: URI parsing (VLESS, SS, Trojan), dedup, filter
- **Checker**: Retry logic, exponential backoff math, IP verification
- **Rotator**: Config building, outbound switching logic
- **Cache**: CRUD operations, DB migrations
- **Config**: TOML parsing, validation

### Integration Tests
- Full flow: Fetch → Parse → Cache → Rotate → Check
- Error scenarios: Source 404, network timeout, invalid node
- State recovery: Crash → Restart → Resume

### Manual Tests
- CLI commands: All modes (run, watch, daemon, interactive)
- SOCKS5 proxy: Real app connections (curl, browser)
- Rotation: Trigger failure, verify rotation
- Node stats: Track latency, success rate over time

### Test Coverage Target
- Core packages: >80% line coverage
- Edge cases: Timeout, invalid input, network errors

---

## 11. Future Extensibility

### Planned Phase 2 Features
- Node quality scoring (latency, speed tests)
- Health check background task
- Metrics endpoint (Prometheus)
- Config hot-reload (SIGHUP)
- Multi-URL monitoring
- Custom node sources (whitelisting)

### Extension Points
- Protocol parsers: Add new protocol → New `parse*()` function
- Sources: Add new source → New `fetch*()` function + integration
- Checking logic: Custom check → Strategy pattern
- Rotation algorithm: Pluggable selector function

---

## Document Control

| Version | Date | Changes |
|---------|------|---------|
| 1.0 | 2026-08-30 | Initial architecture document |

