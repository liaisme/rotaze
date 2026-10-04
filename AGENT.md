# AGENT.md - Rotaze Development Instructions

**Project Name**: Rotaze (IP Rotator with sing-box subprocess)  
**Language**: Go 1.24.7+  
**Platform**: Linux (ARM64, X86)

---

## 🎯 Project Overview

**Rotaze** is a self-healing SOCKS5 proxy server that exposes a local proxy (`127.0.0.1:1080`) and automatically rotates through free VPN tunnels when the target URL becomes unreachable.

### Key Characteristics
- **sing-box subprocess**: Uses external sing-box binary (spawned as subprocess)  
- **Concurrent node fetching**: Fetches from 3 sources in parallel
- **Smart rotation**: Retries with exponential backoff, then rotates to next node
- **Manual control**: CLI commands to list, pick, lock specific tunnels
- **State persistence**: SQLite caching of nodes, stats, current node
- **Multiple run modes**: `run` (one-shot), `watch` (continuous), `daemon` (background), `interactive` (REPL)
- **Systemd integration**: Ready for Linux service deployment

---

## 📋 Development Scope (Phase 1: MVP)

### Must-Have Features ✅
1. **Concurrent Node Fetching**
   - Au1rxx free-vpn-subscriptions (sing-box format)
   - mahdibland SSAggregator (base64 merged)
   - ConfigForge per-country nodes (plaintext URIs)
   - Parallel fetching, deduplication, country filtering

2. **URI Parsing**
   - VLESS (full support: TLS, WS, gRPC, XTLS)
   - Shadowsocks (method, password, plaintext + base64)
   - Trojan (TLS, WS, WebSocket)
   - VMess (basic: UUID, alter_id, TLS, WS)
   - Hysteria2 (password, TLS)
   - Error handling, fallback to next source

3. **sing-box subprocess management**
   - Locates external sing-box binary in PATH, /usr/local/bin, /usr/bin, ~/.local/bin
   - Builds and writes per-instance sing-box config JSON (per port)
   - Manages lifecycle (Start, SwitchOutbound, Close), process reaping, port cleanup
   - Supports OpenVPN materialization (PEM files under data dir)
   - Per-outlet isolation: unique config path per listen port (`singbox-<port>.json`)
   - Port-scoped stale-process cleanup (matches /proc cmdline + listening socket inode)
   - Outbound switching via full stop+restart of the sing-box process
   - SOCKS5 listener on `127.0.0.1:<port>` (default 1080)

4. **URL Monitoring & Rotation**
   - Check HTTP status code via SOCKS5 proxy
   - Retry logic with exponential backoff (1s → 2s → 4s → 10s)
   - Exit IP verification (ipinfo.io) + country check
   - Auto-rotate on failure (pick next good node)
   - Node performance tracking (latency, success rate)

5. **CLI Commands** (Cobra-based)
   - `rotaze run --url <url>` - One-shot check
   - `rotaze watch --url <url>` - Continuous monitoring
   - `rotaze daemon start/stop/status` - Background service
   - `rotaze interactive` - REPL mode
   - `rotaze pool list` - Show available nodes
   - `rotaze use <id|server>` - Manual node selection
   - `rotaze lock on/off` - Lock/unlock to current node
   - `rotaze status` - Show current state

6. **Configuration**
   - TOML config file (`config.example.toml`)
   - Viper-based config loading
   - CLI flags override config
   - Graceful reload support (SIGHUP)

7. **State Persistence**
   - SQLite database (`~/.rotaze/state.db`)
   - Current node tracking (recovery after crash)
   - Node stats (latency, success rate, last check)
   - Rotation history (audit trail)
   - Manual node preferences (lock, exclude, prefer)

8. **Logging & Diagnostics**
   - Structured logging (zap)
   - File + stdout output
   - Debug/info/warn/error levels
   - Rotation history accessible via CLI

---

## 🏗️ Architecture Overview

### High-Level Flow
```
User App
  ↓
SOCKS5 127.0.0.1:1080 (sing-box inbound)
  ↓
Rotaze CLI → sing-box (external subprocess)
  ├─ Fetch nodes (concurrent)
  ├─ Monitor URL (periodic checks)
  ├─ Rotate on failure (smart selection)
  ├─ Cache state (SQLite)
  └─ Expose SOCKS5 ← back to app
  ↓
Internet (via tunnel)
```

### Directory Structure
```
rotaze/
├── cmd/
│   └── rotaze/
│       └── main.go           # Entry point, CLI setup
├── internal/
│   ├── fetcher/              # Node fetching
│   │   ├── sources.go        # Au1rxx, SSAgg, ConfigForge, vpngate
│   │   ├── parser.go         # URI parsing (VLESS, SS, Trojan, etc)
│   │   ├── geoip.go          # Parallel DNS + batched GeoIP enrichment
│   │   ├── ovpn.go           # OpenVPN config download/parsing
│   │   └── pool.go           # Fetch pipeline, dedup, country filter, prune
│   ├── checker/              # URL/IP checking
│   │   ├── checker.go        # Main monitoring loop + backoff retry
│   │   └── ipinfo.go         # Exit IP country verification
│   ├── rotator/              # sing-box subprocess management
│   │   ├── config.go         # Dynamic sing-box config builder
│   │   └── rotator.go        # Subprocess lifecycle, port-scoped cleanup
│   ├── cache/                # SQLite state management (GORM)
│   │   ├── cache.go          # Schema/migrations, batched upsert, node queries
│   │   └── cache_test.go
│   ├── config/               # Configuration management
│   │   └── config.go         # TOML parsing
│   ├── cli/                  # Cobra commands
│   │   ├── root.go           # Root command setup
│   │   ├── run.go            # `rotaze run` command
│   │   ├── watch.go          # `rotaze watch` command
│   │   ├── daemon.go         # `rotaze daemon` commands
│   │   ├── pool.go           # `rotaze pool list` command
│   │   ├── use.go            # `rotaze use` command
│   │   ├── lock.go           # `rotaze lock` command
│   │   ├── status.go         # `rotaze status` command
│   │   ├── interactive.go    # `rotaze interactive` command
│   │   ├── daemon_proxy.go   # `rotaze daemon proxy add/list/rm`
│   │   └── install.go        # `rotaze install-singbox`
│   ├── logger/               # Structured logging
│   │   └── logger.go         # Zap setup, file rotation
│   ├── health/               # Health endpoint
│   │   └── health.go
│   ├── metrics/              # Runtime counters
│   │   └── metrics.go
│   └── version/              # Version metadata
│       └── version.go
├── pkg/
│   └── types/
│       └── types.go          # Shared types (Node, Stats, Config, etc)
├── systemd/
│   └── rotaze.service        # Systemd unit file
├── config.example.toml       # Example configuration
├── go.mod                    # Go dependencies
├── go.sum
├── Makefile                  # Build targets
└── README.md                 # User documentation
```

### Key Packages & Responsibilities

| Package | Responsibility | Dependencies |
|---------|-----------------|--------------|
| `fetcher` | Fetch nodes from sources, parse URIs, dedup, GeoIP enrichment | `net/http`, `encoding/json`, `regexp`, `encoding/base64` |
| `checker` | Check URL via SOCKS5, verify exit IP, retry logic | `fetcher`, `http` (or `curl` subprocess) |
| `rotator` | Manage sing-box subprocess, switch outbounds, port-scoped cleanup | `os/exec`, `encoding/json` |
| `cache` | SQLite state: nodes, stats, current node, lock | `gorm`, `gorm/driver/sqlite` |
| `config` | Load TOML, parse env overrides | `github.com/spf13/viper` |
| `cli` | Cobra commands, user interaction | All above |
| `logger` | Structured logging to file + stdout | `go.uber.org/zap` |

---

## 🎯 Development Phases

### Phase 1: Foundation (Days 1-2)
**Goal**: Core infrastructure + basic functionality

- [ ] Setup Go module, Cobra CLI skeleton
- [ ] Config management (TOML + Viper)
- [ ] Logger setup (Zap structured logging)
- [ ] SQLite schema + migrations
- [ ] Types & data models (Node, Stats, Config)

**Deliverables**:
- `go.mod` with dependencies
- Config example + parsing
- Logger working (stdout + file)
- Empty DB schema

---

### Phase 2: Node Fetching (Days 1-3)
**Goal**: Reliable node pool from 3 sources

- [ ] Implement 3 sources (Au1rxx, SSAgg, ConfigForge)
- [ ] URI parsers (VLESS, SS, Trojan, VMess, Hy2)
- [ ] Concurrent fetching + error handling
- [ ] Deduplication + country filtering
- [ ] Cache nodes to SQLite + TTL management

**Deliverables**:
- `fetcher` package fully functional
- `rotaze run --url https://test.com --country VN` returns nodes
- Proper error logs on source failures
- DB populated with 50+ working nodes

---

### Phase 3: sing-box Integration (Days 3-4)
**Goal**: Run external sing-box binary as a subprocess, expose SOCKS5 locally

- [x] Locate external sing-box binary (PATH, /usr/local/bin, /usr/bin, ~/.local/bin)
- [x] Dynamic config builder (mixed SOCKS5/HTTP inbound + outbound routing)
- [x] Lifecycle: write config → spawn `sing-box run -c <config>` → wait ready → Close
- [x] SOCKS5 listener on 127.0.0.1:1080
- [x] Graceful shutdown + error recovery (waitCh-based process reaping)
- [x] Outbound switching (stop + restart the subprocess with a new outbound)

**Deliverables**:
- sing-box running as an external subprocess
- `curl --socks5 127.0.0.1:1080 https://example.com` works
- Proper error handling

---

### Phase 4: Checking & Rotation (Days 4-5)
**Goal**: Monitor URL, auto-rotate on failure

- [ ] HTTP checker via SOCKS5 (curl or Python subprocess)
- [ ] Exit IP verification (ipinfo.io)
- [ ] Retry logic with exponential backoff
- [ ] Node scoring (latency, success rate)
- [ ] Smart node selection (prefer good performers)
- [ ] Auto-rotation trigger + cooldown for bad nodes
- [ ] State persistence (current node in DB)

**Deliverables**:
- URL stays accessible via rotation
- Logs show check results, rotation reasons
- Node stats updated (latency, success %)
- Recovery from node failure in <10s

---

### Phase 5: CLI Commands (Days 5-6)
**Goal**: User-friendly command interface

- [ ] `rotaze run` - One-shot check + exit
- [ ] `rotaze watch` - Continuous monitoring
- [ ] `rotaze daemon start/stop/status` - Background service
- [ ] `rotaze pool list` - Show available nodes
- [ ] `rotaze use` - Manual node selection
- [ ] `rotaze lock` - Lock/unlock to current node
- [ ] `rotaze status` - Show current state
- [ ] `rotaze interactive` - REPL mode

**Deliverables**:
- All commands work end-to-end
- Proper exit codes (0=success, 1=fail, 2=error)
- Pretty output (tables, colors)
- Help text for all commands

---

### Phase 6: Polish & Testing (Days 6-7)
**Goal**: Production readiness

- [ ] Error handling edge cases
- [ ] Config hot-reload (SIGHUP)
- [ ] Systemd integration
- [ ] Unit tests (core packages)
- [ ] Documentation (README, examples)
- [ ] Cross-compile for Linux (amd64, arm64)

**Deliverables**:
- Single `rotaze` binary (50-80MB)
- Works as systemd service
- Tests passing (>80% coverage)
- Documentation complete

---

## 🔍 Code Quality Standards

### Must-Follow Principles
1. **Error handling**: Every API call must handle errors
   ```go
   if err != nil {
       logger.Error("operation failed", zap.Error(err))
       return err
   }
   ```

2. **Logging**: Info-level for user-facing actions, Debug for diagnostics
   ```go
   logger.Info("Rotating to next node", 
       zap.String("node", node.Tag),
       zap.String("country", node.Country))
   ```

3. **Testing**: Unit tests for core logic (parsers, filters, scoring)
   - Mock network calls (HTTP, DB)
   - Test success + error cases
   - Run: `go test ./...`

4. **Comments**: Explain "why", not "what"
   ```go
   // Exponential backoff ensures we don't hammer failing sources
   backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
   ```

5. **Naming**: Clear, descriptive variable/function names
   - ✅ `rotateToNextNode()`, `checkURLViaSOCKS5()`
   - ❌ `rotate()`, `check()`

6. **Types**: Use typed structs, not `map[string]interface{}`
   ```go
   type Node struct {
       Server   string
       Port     int
       Protocol string
       Country  string
   }
   ```

7. **Concurrency**: Use channels/goroutines safely
   - Context timeouts for all I/O
   - Graceful shutdown handling
   - No goroutine leaks

---

## 🧪 Testing Strategy

### Unit Tests
- **Fetcher**: URI parsing, dedup, filtering
- **Checker**: Retry logic, scoring calculation
- **Cache**: CRUD operations, migrations
- **Config**: TOML parsing, env overrides

### Integration Tests
- Full flow: Fetch → Parse → Cache → Rotate → Check
- Error scenarios: Source 404, network timeout, node failure
- Manual commands: List, Use, Lock, Interactive

### Manual Testing Checklist
- [ ] `rotaze run --url https://example.com --check-ip`
- [ ] `rotaze watch --url https://example.com --interval 30`
- [ ] `rotaze daemon start` → works as systemd service
- [ ] `rotaze pool list` → shows 50+ nodes
- [ ] `rotaze use 5` → switches to node 5, traffic flows
- [ ] `rotaze lock on` → locks to current node
- [ ] `rotaze interactive` → REPL works, commands execute
- [ ] Node rotation on URL failure → within 10s

---

## 🚀 Agent Development Workflow

### Day 1: Setup + Foundation
1. Initialize Go module + Cobra CLI skeleton
2. Setup logger (Zap → file + stdout)
3. Create config system (TOML parsing)
4. Create SQLite schema + models
5. Write basic tests for each

**Commit**: `feat: project scaffold with logger, config, db`

### Day 2-3: Node Fetching
1. Implement fetcher for each source (Au1rxx, SSAgg, ConfigForge)
2. Write URI parsers (VLESS, SS, Trojan, VMess, Hy2)
3. Add concurrent fetching + error handling
4. Implement dedup + country filtering
5. Cache to SQLite, TTL management

**Commit**: `feat: concurrent node fetching from 3 sources`

### Day 3-4: sing-box Integration
1. Locate external sing-box binary (PATH + common install dirs)
2. Build dynamic config (mixed SOCKS5/HTTP inbound + outbound)
3. Implement subprocess lifecycle (spawn, wait-ready, reap) + SOCKS5 listener
4. Test local proxy accessibility
5. Implement outbound switching (stop + restart subprocess)

**Commit**: `feat: sing-box subprocess with SOCKS5 proxy`

### Day 4-5: Checking + Rotation
1. Implement HTTP checker (curl subprocess or Python)
2. Add retry logic with exponential backoff
3. Implement exit IP checker (ipinfo.io)
4. Add node scoring (latency, success rate)
5. Implement auto-rotation on failure

**Commit**: `feat: url monitoring and auto-rotation logic`

### Day 5-6: CLI Commands
1. Implement each command (run, watch, daemon, pool, use, lock, interactive)
2. Add proper output formatting (tables, colors)
3. Test end-to-end scenarios
4. Add help text + examples

**Commit**: `feat: cli commands (run, watch, daemon, pool, etc)`

### Day 6-7: Polish + Tests
1. Error handling edge cases
2. Config hot-reload (SIGHUP)
3. Unit tests + coverage
4. Documentation
5. Cross-compile + release

**Commit**: `test: add unit tests, docs: complete README`

---

## 📝 Code Style Guide

### Go Conventions
- Use `gofmt` for formatting
- Package-level comments for exported types
- Receiver name: `r *Rotator`, `n *Node` (short, clear)
- Error wrapping: `fmt.Errorf("failed to rotate: %w", err)`

### Naming Patterns
- Interfaces: `CheckerInterface`, `RotatorInterface` (optional if clear)
- Private functions: `rotateToNextNode()` (unexported)
- Config structs: `CheckerConfig`, `RotatorConfig`
- Error variables: `ErrNodeNotFound = errors.New("node not found")`

### Example Function
```go
// rotateToNextNode selects the next best-performing node and switches to it.
// It prefers nodes with success rate > 75%, avoiding recently failed ones.
func (r *Rotator) rotateToNextNode(ctx context.Context) (*types.Node, error) {
    nodes, err := r.cache.GetGoodNodes(0.75)  // 75% threshold
    if err != nil {
        r.logger.Error("failed to fetch good nodes", zap.Error(err))
        return nil, fmt.Errorf("fetch good nodes: %w", err)
    }
    
    if len(nodes) == 0 {
        r.logger.Warn("no good nodes available, using all nodes")
        nodes, _ = r.cache.GetAllNodes()
    }
    
    next := nodes[rand.Intn(len(nodes))]
    
    if err := r.box.SwitchOutbound(ctx, next.Tag); err != nil {
        return nil, fmt.Errorf("switch outbound: %w", err)
    }
    
    r.logger.Info("rotated to next node",
        zap.String("node", next.Tag),
        zap.String("server", next.Server),
        zap.Int("port", next.Port))
    
    return next, nil
}
```

---

## 🔄 Development Workflow in Codespaces

### Daily Standup
- Check failing tests: `go test ./...`
- Review logs from last session
- Plan day's work (1-2 features per day)

### Code Review Checklist (Self)
- [ ] All errors handled
- [ ] Logging appropriate (info + debug)
- [ ] No goroutine leaks
- [ ] Tests passing
- [ ] Commit message clear + descriptive

### Git Commit Convention
```
feat: add concurrent node fetching
fix: handle 404 from Au1rxx source
test: add unit tests for URI parsers
docs: update README with examples
refactor: simplify retry logic
```

### Testing Before Commit
```bash
# Run all tests
go test ./... -v

# Check for race conditions
go test ./... -race

# Get coverage
go test ./... -cover

# Format code
gofmt -w .

# Vet for issues
go vet ./...
```

---

## 📚 Reference Documentation

**sing-box**: https://sing-box.sagernet.org/
- External binary, launched as `sing-box run -c <config>`
- JSON config schema (mixed inbound, outbound/dial routing)
- Outbound lifecycle

**Cobra CLI**: https://cobra.dev/
- Commands, flags, subcommands
- Help generation, completion

**Viper Config**: https://github.com/spf13/viper
- TOML parsing, env overrides

**Zap Logger**: https://pkg.go.dev/go.uber.org/zap
- Structured logging, levels

**GORM SQLite**: https://gorm.io/
- Migrations, CRUD operations

---

## 🚨 Critical Success Criteria

1. **External sing-box binary** located automatically (or installed via `rotaze install-singbox`)
2. **SOCKS5 proxy works locally** (127.0.0.1:1080 accessible)
3. **Auto-rotation on URL failure** (<15s recovery)
4. **Smart node selection** (prefers good performers)
5. **Manual tunnel picking** (CLI: `rotaze pool list`, `rotaze use`)
6. **Persistent state** (current node survives restart)
7. **Daemon mode** (systemd integration)
8. **All CLI commands work** (run, watch, daemon, pool, use, lock, interactive)

---

## ✅ Handoff Criteria (Ready for Production)

- [ ] All features implemented (MVP scope)
- [ ] All tests passing (>80% coverage)
- [ ] No goroutine leaks or race conditions
- [ ] Documentation complete (README, examples)
- [ ] Cross-compiled binaries (linux amd64, arm64)
- [ ] Systemd service file working
- [ ] Manual testing checklist passed
- [ ] Graceful error handling (no panics)
- [ ] Performance acceptable (<2s startup, <5s rotation)

---

**Last Updated**: 2026-08-30
**Status**: Ready for Phase 1 (Foundation)  
**Questions**: Refer to PRD.md for detailed requirements

