package cache

import (
	"path/filepath"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

// TestMigrate_DedupesLegacyDuplicates covers opening a database created
// before idx_node_identity existed: it already holds several rows per
// (server, port, protocol). Creating the unique index over those rows fails,
// so migrate() must collapse them first, keeping the lowest id. Without this
// a legacy database cannot be opened at all.
func TestMigrate_DedupesLegacyDuplicates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	c, err := NewCache(dbPath)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	// Recreate the pre-index schema shape.
	if err := c.db.Exec("DROP INDEX IF EXISTS idx_node_identity").Error; err != nil {
		t.Fatalf("drop index: %v", err)
	}
	for _, country := range []string{"VN", "JP", "US"} {
		if err := c.db.Exec(
			"INSERT INTO nodes (server, port, protocol, country, tag) VALUES (?, ?, ?, ?, ?)",
			"1.2.3.4", 1080, "ss", country, "dup").Error; err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	if err := c.db.Exec(
		"INSERT INTO nodes (server, port, protocol, country, tag) VALUES (?, ?, ?, ?, ?)",
		"5.6.7.8", 443, "vmess", "CN", "other").Error; err != nil {
		t.Fatalf("insert distinct row: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewCache(dbPath)
	if err != nil {
		t.Fatalf("reopen legacy database: %v", err)
	}
	defer reopened.Close()

	nodes, err := reopened.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("row count = %d, want 2 (one per distinct identity)", len(nodes))
	}
	for _, n := range nodes {
		if n.Server == "1.2.3.4" && n.Country != "VN" {
			t.Errorf("kept row country = %q, want VN (lowest id wins)", n.Country)
		}
	}

	// The index must now exist, otherwise later upserts can insert duplicates.
	var idx int
	if err := reopened.db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_node_identity'").
		Scan(&idx).Error; err != nil {
		t.Fatalf("check index: %v", err)
	}
	if idx != 1 {
		t.Error("idx_node_identity was not created after dedup")
	}
}

// TestSaveNodes_BatchedUpsert pins the invariants the batched ON CONFLICT
// upsert has to keep: one row per identity however often the node is saved,
// input nodes get their database id back, and every saved node carries a
// fresh fetch time and TTL.
func TestSaveNodes_BatchedUpsert(t *testing.T) {
	c := setupTestCache(t)

	mk := func(server string, country string) *types.Node {
		return &types.Node{Server: server, Port: 1080, Protocol: "ss", Country: country, Tag: "t"}
	}

	// The same identity twice in one call must not create two rows.
	first := mk("1.1.1.1", "VN")
	dup := mk("1.1.1.1", "JP")
	if err := c.SaveNodes([]*types.Node{first, dup}); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	nodes, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("row count = %d, want 1", len(nodes))
	}
	if first.ID == 0 {
		t.Error("SaveNodes did not hydrate the node ID")
	}
	if first.ID != nodes[0].ID {
		t.Errorf("input ID = %d, stored ID = %d", first.ID, nodes[0].ID)
	}
	if first.FetchedAt.IsZero() {
		t.Error("SaveNodes did not set FetchedAt")
	}
	if !first.TTLExpiresAt.After(first.FetchedAt) {
		t.Errorf("TTLExpiresAt = %v, want after FetchedAt %v", first.TTLExpiresAt, first.FetchedAt)
	}

	// Re-saving the same identity updates in place and reuses the id.
	again := mk("1.1.1.1", "US")
	if err := c.SaveNodes([]*types.Node{again}); err != nil {
		t.Fatalf("SaveNodes (second call): %v", err)
	}
	nodes, err = c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("row count after re-save = %d, want 1", len(nodes))
	}
	if nodes[0].Country != "US" {
		t.Errorf("country = %q, want US (re-save must update)", nodes[0].Country)
	}
	if again.ID != nodes[0].ID {
		t.Errorf("re-save ID = %d, stored ID = %d", again.ID, nodes[0].ID)
	}

	// A different identity on the same port and protocol is a distinct node.
	other := mk("2.2.2.2", "CN")
	if err := c.SaveNodes([]*types.Node{other}); err != nil {
		t.Fatalf("SaveNodes (distinct): %v", err)
	}
	nodes, err = c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("row count = %d, want 2 distinct identities", len(nodes))
	}
}
