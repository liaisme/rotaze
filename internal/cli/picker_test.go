package cli

import (
	"strings"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

func TestFilterNodesByCountry(t *testing.T) {
	nodes := []*types.Node{
		{Country: "VN"},
		{Country: "vn"},
		{Country: "CN"},
		{Country: ""},
		{Country: "US"},
	}
	cases := []struct {
		country string
		want    int
	}{
		{"", 5},
		{"all", 5},
		{"ALL", 5},
		{"VN", 2},
		{"vn", 2},
		{"CN", 1},
		{"SG", 0},
	}
	for _, tc := range cases {
		got := filterNodesByCountry(nodes, tc.country)
		if len(got) != tc.want {
			t.Errorf("filterNodesByCountry(%q) = %d nodes, want %d", tc.country, len(got), tc.want)
		}
	}
}

func TestOrderedCandidates(t *testing.T) {
	ssOK := &types.Node{ID: 1, Protocol: "ss", Password: "p", EncryptMethod: "aes-256-gcm", Country: "CN"}
	ssBad := &types.Node{ID: 2, Protocol: "ss", Password: "p", EncryptMethod: "rc4-md5", Country: "CN"}
	vless := &types.Node{ID: 3, Protocol: "vless", UUID: "u", Country: "VN"}
	httpNode := &types.Node{ID: 4, Protocol: "http", Country: "VN"}
	invalid := &types.Node{ID: 5, Protocol: "ss", Country: "US"} // no creds
	preferred := &types.Node{ID: 6, Protocol: "trojan", Password: "tp", Country: "VN"}

	pool := []*types.Node{httpNode, invalid, ssOK, preferred, vless, ssBad}

	t.Run("preferred first, then protocol preference, no dupes", func(t *testing.T) {
		got := orderedCandidates(pool, 6)
		if len(got) != 5 {
			t.Fatalf("expected 5 viable candidates, got %d", len(got))
		}
		wantOrder := []int{6, 1, 3, 4, 2} // preferred, ss-ok, vless, http, ss-bad-method
		for i, id := range wantOrder {
			if got[i].ID != id {
				t.Errorf("position %d: expected node %d, got %d", i, id, got[i].ID)
			}
		}
	})

	t.Run("no preferred keeps pool order by tier", func(t *testing.T) {
		got := orderedCandidates(pool, 0)
		if len(got) != 5 {
			t.Fatalf("expected 5 viable candidates, got %d", len(got))
		}
		// Best SS (supported cipher) first; unsupported-cipher SS is last resort.
		if got[0].ID != 1 {
			t.Errorf("expected supported-cipher SS first, got node %d", got[0].ID)
		}
		if got[len(got)-1].ID != 2 {
			t.Errorf("expected unsupported-cipher SS last, got node %d", got[len(got)-1].ID)
		}
	})

	t.Run("invalid nodes are excluded", func(t *testing.T) {
		got := orderedCandidates([]*types.Node{invalid, ssBad}, 0)
		if len(got) != 1 || got[0].ID != 2 {
			t.Errorf("expected only the valid ss node, got %d nodes", len(got))
		}
	})

	t.Run("empty and all-invalid pools", func(t *testing.T) {
		if got := orderedCandidates(nil, 0); got != nil {
			t.Errorf("expected nil for empty pool, got %v", got)
		}
		if got := orderedCandidates([]*types.Node{invalid}, 0); got != nil {
			t.Errorf("expected nil for all-invalid pool, got %v", got)
		}
	})
}

func TestNodeWithValidCredentials(t *testing.T) {
	tests := []struct {
		name    string
		node    *types.Node
		wantNil bool
	}{
		{"ss with creds", &types.Node{Protocol: "ss", Password: "pw", EncryptMethod: "aes-256-gcm"}, false},
		{"ss missing method", &types.Node{Protocol: "ss", Password: "pw"}, true},
		{"ss missing password", &types.Node{Protocol: "ss", EncryptMethod: "aes-256-gcm"}, true},
		{"vless with uuid", &types.Node{Protocol: "vless", UUID: "u"}, false},
		{"vless no uuid", &types.Node{Protocol: "vless"}, true},
		{"trojan with pw", &types.Node{Protocol: "trojan", Password: "p"}, false},
		{"trojan no pw", &types.Node{Protocol: "trojan"}, true},
		{"vmess with uuid", &types.Node{Protocol: "vmess", UUID: "u"}, false},
		{"vmess no uuid", &types.Node{Protocol: "vmess"}, true},
		{"hy2 with pw", &types.Node{Protocol: "hy2", Password: "p"}, false},
		{"hy2 no pw", &types.Node{Protocol: "hy2"}, true},
		{"http always valid", &types.Node{Protocol: "http"}, false},
		{"unknown protocol", &types.Node{Protocol: "wireguard"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodeWithValidCredentials(tt.node)
			if tt.wantNil && got != nil {
				t.Errorf("expected nil, got %+v", got)
			}
			if !tt.wantNil && got == nil {
				t.Error("expected node, got nil")
			}
		})
	}
}

func TestPickBestNode(t *testing.T) {
	t.Run("empty pool", func(t *testing.T) {
		if got := pickBestNode(nil); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("prefers ss with supported method", func(t *testing.T) {
		nodes := []*types.Node{
			{Protocol: "trojan", Password: "tp"},
			{Protocol: "ss", Password: "sp", EncryptMethod: "aes-256-gcm"},
		}
		got := pickBestNode(nodes)
		if got.Protocol != "ss" {
			t.Errorf("expected ss picked first, got %s", got.Protocol)
		}
	})

	t.Run("falls back to trojan then any credentials", func(t *testing.T) {
		trojanOnly := []*types.Node{{Protocol: "trojan", Password: "tp"}}
		if got := pickBestNode(trojanOnly); got == nil || got.Protocol != "trojan" {
			t.Errorf("expected trojan, got %+v", got)
		}

		httpOnly := []*types.Node{{Protocol: "http"}}
		if got := pickBestNode(httpOnly); got == nil || got.Protocol != "http" {
			t.Errorf("expected http fallback, got %+v", got)
		}
	})

	t.Run("last resort returns first node", func(t *testing.T) {
		nodes := []*types.Node{{Protocol: "ss"}, {Protocol: "vmess"}} // no credentials at all
		got := pickBestNode(nodes)
		if got != nodes[0] {
			t.Errorf("expected first node returned, got %+v", got)
		}
	})
}

func TestPickWorkingNode(t *testing.T) {
	t.Run("db error or empty returns nil", func(t *testing.T) {
		c := setupGlobalCache(t)
		if got := pickWorkingNode(c); got != nil {
			t.Errorf("expected nil for empty pool, got %+v", got)
		}
	})

	t.Run("prefers supported ss over plain ss", func(t *testing.T) {
		c := setupGlobalCache(t)
		seedNode(t, c, &types.Node{Server: "9.9.9.9", Port: 80, Protocol: "ss", Password: "pw", EncryptMethod: "rc4-md5", Country: "VN"})
		seedNode(t, c, &types.Node{Server: "8.8.8.8", Port: 80, Protocol: "ss", Password: "pw", EncryptMethod: "aes-256-gcm", Country: "VN"})

		got := pickWorkingNode(c)
		if got == nil || got.EncryptMethod != "aes-256-gcm" {
			t.Errorf("expected supported-method ss node, got %+v", got)
		}
	})

	t.Run("no viable nodes returns nil", func(t *testing.T) {
		c := setupGlobalCache(t)
		seedNode(t, c, &types.Node{Server: "7.7.7.7", Port: 80, Protocol: "ss", EncryptMethod: "aes-256-gcm", Country: "VN"}) // no password
		if got := pickWorkingNode(c); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})
}

func TestPickSelectedNode(t *testing.T) {
	t.Run("no current node", func(t *testing.T) {
		c := setupGlobalCache(t)
		if got := pickSelectedNode(c); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("current node with credentials", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "6.6.6.6", Port: 8080, Protocol: "http", Country: "VN"})
		if err := c.SetCurrentNode(id); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}
		got := pickSelectedNode(c)
		if got == nil || got.ID != id {
			t.Errorf("expected node %d, got %+v", id, got)
		}
	})

	t.Run("current node missing credentials rejected", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "5.5.5.5", Port: 80, Protocol: "ss", Country: "VN"}) // no password/method
		if err := c.SetCurrentNode(id); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}
		if got := pickSelectedNode(c); got != nil {
			t.Errorf("expected nil for credential-less ss node, got %+v", got)
		}
	})
}

func TestPickNextNode(t *testing.T) {
	t.Run("empty pool errors", func(t *testing.T) {
		c := setupGlobalCache(t)
		if _, err := pickNextNode(c, 0); err == nil {
			t.Error("expected error for empty pool")
		}
	})

	t.Run("skips current node", func(t *testing.T) {
		c := setupGlobalCache(t)
		id1 := seedNode(t, c, &types.Node{Server: "1.1.1.1", Port: 80, Protocol: "http", Country: "VN"})
		id2 := seedNode(t, c, &types.Node{Server: "2.2.2.2", Port: 80, Protocol: "http", Country: "SG"})
		got, err := pickNextNode(c, id1)
		if err != nil {
			t.Fatalf("pickNextNode: %v", err)
		}
		if got.ID != id2 {
			t.Errorf("expected node %d (skip current %d), got %d", id2, id1, got.ID)
		}
	})

	t.Run("skips low success rate", func(t *testing.T) {
		c := setupGlobalCache(t)
		bad := seedNode(t, c, &types.Node{Server: "3.3.3.3", Port: 80, Protocol: "http", Country: "VN"})
		good := seedNode(t, c, &types.Node{Server: "4.4.4.4", Port: 80, Protocol: "http", Country: "SG"})
		// Give bad node a failing history (>5 checks, <25% success)
		for i := 0; i < 8; i++ {
			if err := c.UpdateNodeStats(bad, 100, false); err != nil {
				t.Fatalf("UpdateNodeStats: %v", err)
			}
		}

		got, err := pickNextNode(c, 0)
		if err != nil {
			t.Fatalf("pickNextNode: %v", err)
		}
		if got.ID != good {
			t.Errorf("expected good node %d, got %d", good, got.ID)
		}
	})

	t.Run("prioritizes ss with supported method", func(t *testing.T) {
		c := setupGlobalCache(t)
		seedNode(t, c, &types.Node{Server: "5.5.5.5", Port: 80, Protocol: "http", Country: "VN"})
		seedNode(t, c, &types.Node{Server: "6.6.6.6", Port: 80, Protocol: "ss", Password: "pw", EncryptMethod: "aes-256-gcm", Country: "SG"})
		seedNode(t, c, &types.Node{Server: "7.7.7.7", Port: 80, Protocol: "ss", Password: "pw", EncryptMethod: "chacha20-ietf-poly1305", Country: "US"})

		// With 2 good SS nodes, random pick among them; run repeatedly and
		// confirm result is always one of them (never the http node).
		for i := 0; i < 20; i++ {
			got, err := pickNextNode(c, 0)
			if err != nil {
				t.Fatalf("pickNextNode: %v", err)
			}
			if got.Protocol != "ss" {
				t.Fatalf("expected ss priority, got %s (%s)", got.Protocol, got.Server)
			}
		}
	})
}

func TestNodeDebugString(t *testing.T) {
	cases := []struct {
		name string
		node *types.Node
		want []string // substrings that must appear
		not  []string // substrings that must NOT appear (secrets)
	}{
		{
			"ss shows method and masks password",
			&types.Node{Tag: "t1", Server: "1.2.3.4", Port: 8388, Protocol: "ss", EncryptMethod: "aes-256-gcm", Password: "secretpw", Country: "VN"},
			[]string{"method=aes-256-gcm", "password=***", "server=1.2.3.4:8388", "country=VN"},
			[]string{"secretpw"},
		},
		{
			"trojan has no empty method field",
			&types.Node{Tag: "t2", Server: "5.6.7.8", Port: 443, Protocol: "trojan", Password: "tpw", Country: "VN"},
			[]string{"password=***", "protocol=trojan"},
			[]string{"method=", "tpw"},
		},
		{
			"vless shows missing uuid",
			&types.Node{Tag: "t3", Server: "9.9.9.9", Port: 443, Protocol: "vless", Country: "CN"},
			[]string{"uuid=missing"},
			[]string{},
		},
		{
			"tls and transport detail",
			&types.Node{Tag: "t4", Server: "8.8.8.8", Port: 8443, Protocol: "vless", UUID: "u", TLSEnabled: true, TLSSNI: "example.com", TransportType: "ws", TransportPath: "/path", Country: "SG"},
			[]string{"tls=yes", "sni=example.com", "transport=ws", "path=/path"},
			[]string{},
		},
		{
			"http shows auth when password set",
			&types.Node{Tag: "t5", Server: "3.3.3.3", Port: 8080, Protocol: "http", Password: "hpw", Country: "US"},
			[]string{"auth=***"},
			[]string{"hpw"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeDebugString(tc.node)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("nodeDebugString missing %q in %q", want, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("nodeDebugString leaked %q in %q", n, got)
				}
			}
		})
	}
}
