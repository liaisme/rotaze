// Rotaze - Self-healing SOCKS5 proxy with automatic IP rotation.
//
// Usage:
//
//	rotaze run --url https://example.com
//	rotaze watch --url https://example.com
//	rotaze daemon start
//	rotaze pool list
//	rotaze use 5
//	rotaze interactive
package main

import (
	"github.com/rotaze/rotaze/internal/cli"
)

func main() {
	cli.Execute()
}
