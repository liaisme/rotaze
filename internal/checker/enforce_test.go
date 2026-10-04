package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
)

// startSocksRelay runs a minimal SOCKS5 (no-auth) relay that forwards
// CONNECT requests to their target. Used to route checker traffic and
// exit-IP lookups through a controlled local proxy.
func startSocksRelay(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("relay listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				handleSocksConnect(c)
			}(conn)
		}
	}()

	return ln.Addr().String()
}

// handleSocksConnect performs the SOCKS5 greeting + CONNECT handshake,
// then blindly proxies bytes between the client and the target.
func handleSocksConnect(c net.Conn) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 512)

	// Greeting: VER, NMETHODS, METHODS
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return
	}
	// Reply: no auth
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// Request: VER, CMD, RSV, ATYP, ADDR, PORT
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	atyp := buf[3]
	var addr string
	switch atyp {
	case 0x01: // IPv4
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		addr = net.IP(buf[:4]).String()
	case 0x03: // domain
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(c, buf[:l]); err != nil {
			return
		}
		addr = string(buf[:l])
	case 0x04: // IPv6
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return
		}
		addr = net.IP(buf[:16]).String()
	default:
		return
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])

	target, err := net.Dial("tcp", net.JoinHostPort(addr, fmt.Sprintf("%d", port)))
	if err != nil {
		c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	c.SetDeadline(time.Time{})
	target.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { io.Copy(target, c); done <- struct{}{} }()
	go func() { io.Copy(c, target); done <- struct{}{} }()
	<-done
}

// providerStub returns an ipProvider backed by an httptest server.
func providerStub(t *testing.T, info IPInfo) []ipProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(info)
	}))
	t.Cleanup(srv.Close)
	return []ipProvider{{
		name: "stub",
		url:  srv.URL,
		decode: func(b []byte) (IPInfo, error) {
			var out IPInfo
			if err := json.Unmarshal(b, &out); err != nil {
				return IPInfo{}, err
			}
			return out, nil
		},
	}}
}

func TestCheck_CountryEnforcement(t *testing.T) {
	relay := startSocksRelay(t)
	urlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(urlSrv.Close)

	dbPath := t.TempDir() + "/enforce.db"
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SetCurrentNode(1); err != nil {
		t.Fatal(err)
	}

	t.Run("matching country passes", func(t *testing.T) {
		ch := New(c, zap.NewNop(), &Config{
			URL:        urlSrv.URL,
			Timeout:    5 * time.Second,
			MaxRetries: 0,
			CheckIP:    true,
			Country:    "VN",
			SOCKS5Addr: relay,
		})
		orig := ipProviders
		ipProviders = providerStub(t, IPInfo{IP: "1.2.3.4", Country: "VN"})
		defer func() { ipProviders = orig }()

		if err := ch.check(context.Background()); err != nil {
			t.Fatalf("check with matching country: %v", err)
		}
	})

	t.Run("mismatch country rotates", func(t *testing.T) {
		rotated := false
		ch := New(c, zap.NewNop(), &Config{
			URL:        urlSrv.URL,
			Timeout:    5 * time.Second,
			MaxRetries: 0,
			CheckIP:    true,
			Country:    "VN",
			SOCKS5Addr: relay,
		})
		orig := ipProviders
		ipProviders = providerStub(t, IPInfo{IP: "9.9.9.9", Country: "SG"})
		defer func() { ipProviders = orig }()
		ch.OnRotate(func(ctx context.Context, reason string) error {
			rotated = true
			return nil
		})

		err := ch.check(context.Background())
		if err == nil {
			t.Fatal("expected error on country mismatch")
		}
		if !rotated {
			t.Error("expected rotation callback on country mismatch")
		}
	})

	t.Run("verification disabled skips check", func(t *testing.T) {
		ch := New(c, zap.NewNop(), &Config{
			URL:        urlSrv.URL,
			Timeout:    5 * time.Second,
			MaxRetries: 0,
			CheckIP:    false, // disabled
			Country:    "VN",
			SOCKS5Addr: relay,
		})
		// Provider stub that would fail on request; verification must never run.
		orig := ipProviders
		ipProviders = providerStub(t, IPInfo{Country: ""})
		defer func() { ipProviders = orig }()

		if err := ch.check(context.Background()); err != nil {
			t.Fatalf("check with verification disabled: %v", err)
		}
	})
}
