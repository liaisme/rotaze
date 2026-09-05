# Contributing to Rotaze

Thank you for your interest in contributing to Rotaze! This document provides guidelines and instructions for contributing.

## Table of Contents

- [Code of Conduct](#code-of-conduct)
- [Getting Started](#getting-started)
- [Development Setup](#development-setup)
- [Project Structure](#project-structure)
- [Making Changes](#making-changes)
- [Testing](#testing)
- [Pull Requests](#pull-requests)
- [Style Guide](#style-guide)

## Code of Conduct

Please be respectful and constructive in all interactions. We're here to build great software together.

## Getting Started

1. **Fork the repository** on GitHub
2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/rotaze.git
   cd rotaze
   ```
3. **Add upstream remote**:
   ```bash
   git remote add upstream https://github.com/rotaze/rotaze.git
   ```

## Development Setup

### Prerequisites

- Go 1.22 or later
- sing-box (for testing proxy functionality)
- Git

### Setup

1. **Install dependencies**:
   ```bash
   go mod download
   ```

2. **Install sing-box** (for testing):
   ```bash
   # Linux
   curl -sL https://github.com/SagerNet/sing-box/releases/download/v1.13.19/sing-box-1.13.19-linux-amd64.tar.gz | tar xz
   sudo mv sing-box-*/sing-box /usr/local/bin/
   
   # macOS
   brew install sing-box
   ```

3. **Build the project**:
   ```bash
   make build
   ```

4. **Run tests**:
   ```bash
   make test
   ```

## Project Structure

```
rotaze/
├── cmd/rotaze/           # Entry point
├── internal/
│   ├── cache/            # SQLite state management
│   ├── checker/          # URL monitoring + retry logic
│   ├── cli/              # Cobra commands
│   ├── config/           # TOML config loading
│   ├── fetcher/          # Node fetching + URI parsing
│   ├── logger/           # Zap structured logging
│   └── rotator/          # sing-box process management
├── pkg/types/            # Shared data types
├── systemd/              # Systemd service files
├── Dockerfile            # Container build
└── Makefile              # Build targets
```

## Making Changes

### 1. Create a Branch

```bash
git checkout -b feature/your-feature-name
# or
git checkout -b fix/issue-number-description
```

### 2. Make Your Changes

- Follow the [Style Guide](#style-guide)
- Keep commits focused and atomic
- Write clear commit messages

### 3. Test Your Changes

```bash
# Run all tests
make test

# Run tests with race detector
make test-race

# Run tests with coverage
make test-cover

# Build and test manually
make build
./build/rotaze run --url https://api.ipify.org
```

### 4. Commit Your Changes

```bash
git add .
git commit -m "feat: add amazing new feature

- Detailed description of what was added
- Why it was needed
- Any caveats or considerations

Closes #123"
```

## Testing

### Running Tests

```bash
# Unit tests
make test

# With race detector
make test-race

# With coverage report
make test-cover

# Specific package
go test ./internal/fetcher/... -v
```

### Writing Tests

- Place test files alongside source files (e.g., `parser.go` → `parser_test.go`)
- Use table-driven tests for multiple cases
- Mock external dependencies when possible
- Aim for >80% coverage on new code

Example test:

```go
func TestParseVLESS(t *testing.T) {
    tests := []struct {
        name    string
        uri     string
        want    *types.Node
        wantErr bool
    }{
        {
            name: "valid vless with tls",
            uri:  "vless://uuid@server:443?security=tls#tag",
            want: &types.Node{
                Protocol: "vless",
                UUID:     "uuid",
                Server:   "server",
                Port:     443,
                TLSEnabled: true,
            },
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseURI(tt.uri)
            if (err != nil) != tt.wantErr {
                t.Errorf("ParseURI() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !tt.wantErr && got.Protocol != tt.want.Protocol {
                t.Errorf("ParseURI() protocol = %v, want %v", got.Protocol, tt.want.Protocol)
            }
        })
    }
}
```

## Pull Requests

### Before Submitting

1. **Update documentation** if needed
2. **Add tests** for new functionality
3. **Ensure all tests pass**: `make test`
4. **Run linter**: `make lint` (if golangci-lint installed)
5. **Update CHANGELOG.md** (if exists)

### PR Template

```markdown
## Description

Brief description of changes.

## Type of Change

- [ ] Bug fix
- [ ] New feature
- [ ] Breaking change
- [ ] Documentation update

## Testing

- [ ] Unit tests added/updated
- [ ] Manual testing performed
- [ ] All tests passing

## Checklist

- [ ] Code follows project style
- [ ] Self-reviewed code
- [ ] Comments added for complex code
- [ ] Documentation updated
- [ ] No new warnings
```

### Review Process

1. PR will be reviewed by maintainers
2. Address feedback and make requested changes
3. Once approved, PR will be merged

## Style Guide

### Go Code

- Follow [Effective Go](https://go.dev/doc/effective-go) guidelines
- Use `gofmt` to format code
- Keep functions focused and short
- Use meaningful variable names
- Add comments for exported functions

### Commit Messages

- Use conventional commits format:
  - `feat:` for new features
  - `fix:` for bug fixes
  - `docs:` for documentation
  - `test:` for tests
  - `refactor:` for refactoring
  - `chore:` for maintenance

Example:
```
feat: add VLESS transport support

- Added WebSocket transport parsing
- Added gRPC transport parsing
- Updated sing-box config generation

Closes #456
```

### File Naming

- Use lowercase with underscores for Go files
- Test files end with `_test.go`
- Keep related files in the same package

## Questions?

If you have questions:
1. Check existing issues and discussions
2. Open a new issue with the `question` label
3. Join our community chat (if available)

Thank you for contributing! 🎉
