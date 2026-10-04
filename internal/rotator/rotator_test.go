package rotator

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"go.uber.org/zap"

	"github.com/rotaze/rotaze/pkg/types"
)

func newTestRotator(t *testing.T, port int) *Rotator {
	t.Helper()
	return New(zap.NewNop(), &Config{
		ListenAddr: "127.0.0.1",
		ListenPort: port,
		DataDir:    t.TempDir(),
	})
}

func testNode(protocol string) *types.Node {
	return &types.Node{
		ID:       1,
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: protocol,
		Tag:      "test-" + protocol,
	}
}

// nodeWithValidCredentials returns a node of the given protocol carrying the
// fields that protocol requires, so config generation is exercised end to end.
func nodeWithValidCredentials(protocol string) *types.Node {
	node := testNode(protocol)
	node.UUID = "b831381d-6324-4d53-ad4f-8cda48b30811"
	node.Password = "s3cr3t-p4ssw0rd"
	node.EncryptMethod = "aes-256-gcm"
	node.AlterID = 0
	switch protocol {
	case "ss":
		node.EncryptMethod = "aes-256-gcm"
	case "vmess":
		node.EncryptMethod = "auto"
	case "hy2", "trojan":
		node.TLSEnabled = true
		node.TLSSNI = "example.com"
	case "vless":
		node.TLSEnabled = true
		node.TLSSNI = "example.com"
	}
	return node
}

func TestRotator_New(t *testing.T) {
	r := newTestRotator(t, 1080)
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.listenAddr != "127.0.0.1" {
		t.Errorf("listenAddr = %q", r.listenAddr)
	}
	if r.listenPort != 1080 {
		t.Errorf("listenPort = %d", r.listenPort)
	}
	if r.IsRunning() {
		t.Error("fresh rotator should not be running")
	}
}

func TestRotator_GetListenAddr(t *testing.T) {
	r := New(zap.NewNop(), &Config{ListenAddr: "127.0.0.1", ListenPort: 9999})
	if got := r.GetListenAddr(); got != "127.0.0.1:9999" {
		t.Errorf("GetListenAddr() = %q, want 127.0.0.1:9999", got)
	}
}

func TestRotator_Start_NoDataDir(t *testing.T) {
	// DataDir points at a path that cannot be created (file instead of dir)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	r := New(zap.NewNop(), &Config{
		ListenAddr: "127.0.0.1",
		ListenPort: 1080,
		DataDir:    filepath.Join(blocker, "sub"),
	})
	err := r.Start(context.Background(), testNode("ss"))
	if err == nil {
		t.Error("Start with unwritable DataDir should fail")
		r.Close()
	}
}

func TestRotator_Close_NotRunning(t *testing.T) {
	r := newTestRotator(t, 1080)
	// Close on non-started rotator should not error or panic
	if err := r.Close(); err != nil {
		t.Errorf("Close() on idle rotator returned error: %v", err)
	}
}

// TestRotator_Start_PortHeldByForeignListener verifies the startup failure
// path: a port another process holds must produce a clear error and must not
// leave the rotator reporting itself as running.
func TestRotator_Start_PortHeldByForeignListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	r := newTestRotator(t, port)
	if err := r.Start(context.Background(), nodeWithValidCredentials("ss")); err == nil {
		_ = r.Close()
		t.Fatal("Start on an occupied port should fail")
	}
	if r.IsRunning() {
		t.Error("rotator must not report running after a failed start")
	}
}

func TestRotator_Lifecycle_Embedded(t *testing.T) {
	r := newTestRotator(t, 19080)
	ctx := context.Background()

	node := nodeWithValidCredentials("http")
	node.Server = "127.0.0.1"
	node.Port = 1 // dead upstream, but sing-box itself should start

	if err := r.Start(ctx, node); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.IsRunning() {
		t.Error("IsRunning should be true after Start")
	}
	if got := r.GetListenAddr(); got != "127.0.0.1:19080" {
		t.Errorf("GetListenAddr = %q", got)
	}
	assertListening(t, "127.0.0.1", 19080, true)

	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if r.IsRunning() {
		t.Error("IsRunning should be false after Close")
	}
	// Close must release the port so a later rotator (or outlet) can rebind.
	assertListening(t, "127.0.0.1", 19080, false)

	// Double close should be safe
	if err := r.Close(); err != nil {
		t.Errorf("double Close returned error: %v", err)
	}
}

func TestRotator_SwitchOutbound_Embedded(t *testing.T) {
	r := newTestRotator(t, 19081)
	ctx := context.Background()

	first := nodeWithValidCredentials("http")
	first.Server = "127.0.0.1"
	first.Port = 1
	first.Tag = "switch-first"

	if err := r.Start(ctx, first); err != nil {
		t.Fatalf("Start: %v", err)
	}

	second := nodeWithValidCredentials("http")
	second.Server = "127.0.0.2"
	second.Port = 2
	second.Tag = "switch-second"

	if err := r.SwitchOutbound(ctx, second); err != nil {
		t.Fatalf("SwitchOutbound: %v", err)
	}
	if !r.IsRunning() {
		t.Error("rotator should be running after SwitchOutbound")
	}
	// The replacement box must still serve on the same port.
	assertListening(t, "127.0.0.1", 19081, true)

	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if r.IsRunning() {
		t.Error("rotator should not be running after Close")
	}
}

// TestRotator_ConfigAcceptedBySingBox is the guard against config/schema
// drift: every protocol rotaze can start must be accepted by sing-box's own
// JSON parser, because that parse is the embedded startup path. A field rename
// or removal upstream breaks this test instead of every proxy at runtime.
func TestRotator_ConfigAcceptedBySingBox(t *testing.T) {
	ctx := include.Context(service.ExtendContext(context.Background()))

	protocols := []string{"ss", "vless", "vmess", "trojan", "hy2", "http", "socks"}
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			configJSON := BuildConfig(nodeWithValidCredentials(protocol), "127.0.0.1", 1080)
			options, err := json.UnmarshalExtendedContext[option.Options](ctx, configJSON)
			if err != nil {
				t.Fatalf("sing-box rejected generated %s config: %v\nconfig:\n%s",
					protocol, err, configJSON)
			}
			if len(options.Outbounds) == 0 {
				t.Errorf("%s config produced no outbounds", protocol)
			}
			if len(options.Inbounds) != 1 {
				t.Errorf("%s config produced %d inbounds, want 1", protocol, len(options.Inbounds))
			}
		})
	}
}

// TestRotator_OVPNConfigAcceptedBySingBox covers the OpenVPN endpoint path,
// which replaces outbounds with an endpoints[] entry.
func TestRotator_OVPNConfigAcceptedBySingBox(t *testing.T) {
	ctx := include.Context(service.ExtendContext(context.Background()))

	node := nodeWithValidCredentials("ovpn")
	node.Password = "vpnpass"
	node.AuthMethod = "SHA1"
	node.EncryptMethod = "AES-128-CBC"
	node.TransportType = "tcp"

	configJSON := BuildConfig(node, "127.0.0.1", 1080)
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, configJSON)
	if err != nil {
		t.Fatalf("sing-box rejected generated ovpn config: %v\nconfig:\n%s", err, configJSON)
	}
	if len(options.Endpoints) != 1 {
		t.Errorf("ovpn config produced %d endpoints, want 1", len(options.Endpoints))
	}
}

// assertListening checks whether a TCP connect to addr succeeds.
func assertListening(t *testing.T, host string, port int, want bool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err == nil {
		conn.Close()
	}
	if want && err != nil {
		t.Errorf("expected %s to accept connections: %v",
			net.JoinHostPort(host, strconv.Itoa(port)), err)
	}
	if !want && err == nil {
		t.Errorf("expected %s to be closed, but it accepted a connection",
			net.JoinHostPort(host, strconv.Itoa(port)))
	}
}
