package cache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rotaze/rotaze/pkg/types"
)

func setupTestCache(t *testing.T) *Cache {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := NewCache(dbPath)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ============================================================
// NewCache / Close
// ============================================================

func TestNewCache(t *testing.T) {
	c := setupTestCache(t)
	if c == nil {
		t.Fatal("NewCache returned nil")
	}
}

func TestNewCache_InvalidPath(t *testing.T) {
	// Create a temp dir and make it read-only to simulate invalid path
	tmp := t.TempDir()
	ro := filepath.Join(tmp, "ro")
	if err := os.Mkdir(ro, 0555); err != nil {
		t.Fatal(err)
	}
	// Try to create db in read-only directory
	bad := filepath.Join(ro, "subdir", "test.db")
	_, err := NewCache(bad)
	if err == nil {
		// Try to make writable for cleanup if needed
		_ = os.Chmod(ro, 0755)
		t.Error("expected error for invalid path")
	}
	_ = os.Chmod(ro, 0755)
}

func TestClose(t *testing.T) {
	c := setupTestCache(t)
	if err := c.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestClose_MultipleTimes(t *testing.T) {
	c := setupTestCache(t)
	c.Close()
	// Second close should not panic
	c.Close()
}

// ============================================================
// SaveNodes
// ============================================================

func TestSaveNodes_Insert(t *testing.T) {
	c := setupTestCache(t)

	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss", Country: "US"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan", Country: "JP"},
	}

	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes() error = %v", err)
	}

	got, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes() error = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("GetNodes() returned %d nodes, want 3", len(got))
	}
}

func TestSaveNodes_Upsert(t *testing.T) {
	c := setupTestCache(t)

	// Insert
	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN", Tag: "old-tag"}
	if err := c.SaveNodes([]*types.Node{node}); err != nil {
		t.Fatal(err)
	}

	// Upsert same server:port:protocol
	updated := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "US", Tag: "new-tag"}
	if err := c.SaveNodes([]*types.Node{updated}); err != nil {
		t.Fatal(err)
	}

	count, _ := c.CountNodes()
	if count != 1 {
		t.Errorf("CountNodes() = %d, want 1 (should upsert)", count)
	}

	nodes, _ := c.GetNodes()
	if nodes[0].Tag != "new-tag" {
		t.Errorf("Tag = %s, want new-tag", nodes[0].Tag)
	}
	if nodes[0].Country != "US" {
		t.Errorf("Country = %s, want US", nodes[0].Country)
	}
}

func TestSaveNodes_Deduplication(t *testing.T) {
	c := setupTestCache(t)

	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless"}
	c.SaveNodes([]*types.Node{node})
	c.SaveNodes([]*types.Node{node})
	c.SaveNodes([]*types.Node{node})

	count, _ := c.CountNodes()
	if count != 1 {
		t.Errorf("CountNodes() after 3 saves of same node = %d, want 1", count)
	}
}

func TestSaveNodes_Empty(t *testing.T) {
	c := setupTestCache(t)
	if err := c.SaveNodes(nil); err != nil {
		t.Errorf("SaveNodes(nil) error = %v", err)
	}
	count, _ := c.CountNodes()
	if count != 0 {
		t.Errorf("CountNodes() = %d, want 0", count)
	}
}

func TestSaveNodes_SetsTTL(t *testing.T) {
	c := setupTestCache(t)

	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless"}
	c.SaveNodes([]*types.Node{node})

	nodes, _ := c.GetNodes()
	if nodes[0].TTLExpiresAt.Before(time.Now()) {
		t.Error("TTL should be in the future")
	}
}

func TestSaveNodes_SetsFetchedAt(t *testing.T) {
	c := setupTestCache(t)

	before := time.Now()
	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless"}
	c.SaveNodes([]*types.Node{node})

	nodes, _ := c.GetNodes()
	if nodes[0].FetchedAt.Before(before) {
		t.Error("FetchedAt should be set")
	}
}

// ============================================================
// GetNodes / GetNodeByID
// ============================================================

func TestGetNodes_Empty(t *testing.T) {
	c := setupTestCache(t)
	nodes, err := c.GetNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("GetNodes() on empty db returned %d", len(nodes))
	}
}

func TestGetNodeByID_Found(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless", Tag: "test"}})

	nodes, _ := c.GetNodes()
	node, err := c.GetNodeByID(nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if node.Server != "1.2.3.4" {
		t.Errorf("Server = %s, want 1.2.3.4", node.Server)
	}
}

func TestGetNodeByID_NotFound(t *testing.T) {
	c := setupTestCache(t)
	_, err := c.GetNodeByID(99999)
	if err == nil {
		t.Error("expected error for non-existent node")
	}
}

// ============================================================
// GetNodesByCountry / GetNodesByProtocol
// ============================================================

func TestGetNodesByCountry(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
		{Server: "5.6.7.8", Port: 443, Protocol: "ss", Country: "US"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan", Country: "VN"},
	})

	vn, _ := c.GetNodesByCountry("VN")
	if len(vn) != 2 {
		t.Errorf("GetNodesByCountry(VN) returned %d, want 2", len(vn))
	}

	us, _ := c.GetNodesByCountry("US")
	if len(us) != 1 {
		t.Errorf("GetNodesByCountry(US) returned %d, want 1", len(us))
	}
}

func TestGetNodesByCountry_NotFound(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"}})

	nodes, _ := c.GetNodesByCountry("ZZ")
	if len(nodes) != 0 {
		t.Errorf("GetNodesByCountry(ZZ) returned %d, want 0", len(nodes))
	}
}

func TestGetNodesByProtocol(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
		{Server: "9.10.11.12", Port: 443, Protocol: "vless"},
	})

	vless, _ := c.GetNodesByProtocol("vless")
	if len(vless) != 2 {
		t.Errorf("GetNodesByProtocol(vless) returned %d, want 2", len(vless))
	}

	ss, _ := c.GetNodesByProtocol("ss")
	if len(ss) != 1 {
		t.Errorf("GetNodesByProtocol(ss) returned %d, want 1", len(ss))
	}
}

// ============================================================
// DeleteNodes / DeleteNodeStats
// ============================================================

func TestDeleteNodes(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan"},
	})

	nodes, _ := c.GetNodes()
	ids := []int{nodes[0].ID, nodes[2].ID}

	if err := c.DeleteNodes(ids); err != nil {
		t.Fatal(err)
	}

	count, _ := c.CountNodes()
	if count != 1 {
		t.Errorf("CountNodes() after delete = %d, want 1", count)
	}

	remaining, _ := c.GetNodes()
	if remaining[0].Server != "5.6.7.8" {
		t.Errorf("remaining node server = %s, want 5.6.7.8", remaining[0].Server)
	}
}

func TestDeleteNodes_Empty(t *testing.T) {
	c := setupTestCache(t)
	if err := c.DeleteNodes(nil); err != nil {
		t.Errorf("DeleteNodes(nil) error = %v", err)
	}
	if err := c.DeleteNodes([]int{}); err != nil {
		t.Errorf("DeleteNodes([]) error = %v", err)
	}
}

func TestDeleteNodeStats(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
	})

	nodes, _ := c.GetNodes()
	c.UpdateNodeStats(nodes[0].ID, 100, true)
	c.UpdateNodeStats(nodes[1].ID, 200, true)

	// Delete stats for first node
	if err := c.DeleteNodeStats([]int{nodes[0].ID}); err != nil {
		t.Fatal(err)
	}

	_, err := c.GetNodeStats(nodes[0].ID)
	if err == nil {
		t.Error("expected error for deleted stats")
	}

	stats, err := c.GetNodeStats(nodes[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LatencyMS != 200 {
		t.Errorf("LatencyMS = %d, want 200", stats.LatencyMS)
	}
}

func TestDeleteNodeStats_Empty(t *testing.T) {
	c := setupTestCache(t)
	if err := c.DeleteNodeStats(nil); err != nil {
		t.Errorf("DeleteNodeStats(nil) error = %v", err)
	}
}

// ============================================================
// GetDuplicateGroups
// ============================================================

// ============================================================
// GetNodeCountByProtocol / GetNodeCountByCountry
// ============================================================

func TestGetNodeCountByProtocol(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
		{Server: "9.10.11.12", Port: 443, Protocol: "vless"},
	})

	counts, err := c.GetNodeCountByProtocol()
	if err != nil {
		t.Fatal(err)
	}

	if counts["vless"] != 2 {
		t.Errorf("vless count = %d, want 2", counts["vless"])
	}
	if counts["ss"] != 1 {
		t.Errorf("ss count = %d, want 1", counts["ss"])
	}
}

func TestGetNodeCountByCountry(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss", Country: "US"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan", Country: "VN"},
	})

	counts, err := c.GetNodeCountByCountry()
	if err != nil {
		t.Fatal(err)
	}

	if counts["VN"] != 2 {
		t.Errorf("VN count = %d, want 2", counts["VN"])
	}
	if counts["US"] != 1 {
		t.Errorf("US count = %d, want 1", counts["US"])
	}
}

// ============================================================
// CountNodes
// ============================================================

func TestCountNodes_Empty(t *testing.T) {
	c := setupTestCache(t)
	count, _ := c.CountNodes()
	if count != 0 {
		t.Errorf("CountNodes() on empty = %d, want 0", count)
	}
}

func TestCountNodes(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
	})

	count, _ := c.CountNodes()
	if count != 2 {
		t.Errorf("CountNodes() = %d, want 2", count)
	}
}

// ============================================================
// State Operations
// ============================================================

func TestSetState_GetState(t *testing.T) {
	c := setupTestCache(t)

	c.SetState("test-key", "test-value")
	val, err := c.GetState("test-key")
	if err != nil {
		t.Fatal(err)
	}
	if val != "test-value" {
		t.Errorf("GetState() = %s, want test-value", val)
	}
}

func TestGetState_NotFound(t *testing.T) {
	c := setupTestCache(t)
	val, err := c.GetState("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("GetState(nonexistent) = %s, want empty", val)
	}
}

func TestSetState_Overwrite(t *testing.T) {
	c := setupTestCache(t)

	c.SetState("key", "old")
	c.SetState("key", "new")

	val, _ := c.GetState("key")
	if val != "new" {
		t.Errorf("GetState() = %s, want new", val)
	}
}

func TestSetCurrentNode_GetCurrentNode(t *testing.T) {
	c := setupTestCache(t)

	c.SetCurrentNode(42)
	id, err := c.GetCurrentNode()
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Errorf("GetCurrentNode() = %d, want 42", id)
	}
}

func TestGetCurrentNode_Empty(t *testing.T) {
	c := setupTestCache(t)
	id, err := c.GetCurrentNode()
	if err != nil {
		t.Fatal(err)
	}
	if id != 0 {
		t.Errorf("GetCurrentNode() on empty = %d, want 0", id)
	}
}

func TestLockUnlockNode(t *testing.T) {
	c := setupTestCache(t)

	// Initially unlocked
	locked, _ := c.IsNodeLocked()
	if locked {
		t.Error("IsNodeLocked() should be false initially")
	}

	// Lock
	c.LockNode(5)
	locked, _ = c.IsNodeLocked()
	if !locked {
		t.Error("IsNodeLocked() should be true after LockNode()")
	}

	lid, _ := c.GetLockedNode()
	if lid != 5 {
		t.Errorf("GetLockedNode() = %d, want 5", lid)
	}

	// Unlock
	c.UnlockNode()
	locked, _ = c.IsNodeLocked()
	if locked {
		t.Error("IsNodeLocked() should be false after UnlockNode()")
	}
}

func TestGetLockedNode_Empty(t *testing.T) {
	c := setupTestCache(t)
	id, err := c.GetLockedNode()
	if err != nil {
		t.Fatal(err)
	}
	if id != 0 {
		t.Errorf("GetLockedNode() on empty = %d, want 0", id)
	}
}

// ============================================================
// NodeStats
// ============================================================

func TestUpdateNodeStats_FirstCheck(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	nodes, _ := c.GetNodes()
	if err := c.UpdateNodeStats(nodes[0].ID, 100, true); err != nil {
		t.Fatal(err)
	}

	stats, _ := c.GetNodeStats(nodes[0].ID)
	if stats.TotalChecks != 1 {
		t.Errorf("TotalChecks = %d, want 1", stats.TotalChecks)
	}
	if stats.TotalFailures != 0 {
		t.Errorf("TotalFailures = %d, want 0", stats.TotalFailures)
	}
	if stats.LatencyMS != 100 {
		t.Errorf("LatencyMS = %d, want 100", stats.LatencyMS)
	}
	if stats.SuccessRate != 1.0 {
		t.Errorf("SuccessRate = %f, want 1.0", stats.SuccessRate)
	}
}

func TestUpdateNodeStats_FirstCheck_Failure(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	nodes, _ := c.GetNodes()
	c.UpdateNodeStats(nodes[0].ID, 0, false)

	stats, _ := c.GetNodeStats(nodes[0].ID)
	if stats.TotalFailures != 1 {
		t.Errorf("TotalFailures = %d, want 1", stats.TotalFailures)
	}
	if stats.SuccessRate != 0.0 {
		t.Errorf("SuccessRate = %f, want 0.0", stats.SuccessRate)
	}
}

func TestUpdateNodeStats_SuccessiveChecks(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	nodes, _ := c.GetNodes()
	nid := nodes[0].ID

	// 3 checks: success, fail, success
	c.UpdateNodeStats(nid, 100, true)
	c.UpdateNodeStats(nid, 0, false)
	c.UpdateNodeStats(nid, 150, true)

	stats, _ := c.GetNodeStats(nid)
	if stats.TotalChecks != 3 {
		t.Errorf("TotalChecks = %d, want 3", stats.TotalChecks)
	}
	if stats.TotalFailures != 1 {
		t.Errorf("TotalFailures = %d, want 1", stats.TotalFailures)
	}
	if stats.SuccessRate != 2.0/3.0 {
		t.Errorf("SuccessRate = %f, want %f", stats.SuccessRate, 2.0/3.0)
	}
	if stats.LatencyMS != 150 {
		t.Errorf("LatencyMS = %d, want 150", stats.LatencyMS)
	}
}

func TestUpdateNodeStats_ConsecutiveFailures(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	nodes, _ := c.GetNodes()
	nid := nodes[0].ID

	c.UpdateNodeStats(nid, 0, false)
	c.UpdateNodeStats(nid, 0, false)
	c.UpdateNodeStats(nid, 0, false)

	stats, _ := c.GetNodeStats(nid)
	// First call creates with ConsecutiveFailures=0, second and third increment
	if stats.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2", stats.ConsecutiveFailures)
	}

	// Success resets consecutive failures
	c.UpdateNodeStats(nid, 100, true)
	stats, _ = c.GetNodeStats(nid)
	if stats.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after success = %d, want 0", stats.ConsecutiveFailures)
	}
}

func TestGetNodeStats_NotFound(t *testing.T) {
	c := setupTestCache(t)
	_, err := c.GetNodeStats(99999)
	if err == nil {
		t.Error("expected error for non-existent stats")
	}
}

func TestGetNodeStatsAll(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
	})

	nodes, _ := c.GetNodes()
	c.UpdateNodeStats(nodes[0].ID, 100, true)
	c.UpdateNodeStats(nodes[1].ID, 200, false)

	all, err := c.GetNodeStatsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("GetNodeStatsAll() returned %d, want 2", len(all))
	}
}

func TestGetNodeStatsAll_Empty(t *testing.T) {
	c := setupTestCache(t)
	all, _ := c.GetNodeStatsAll()
	if len(all) != 0 {
		t.Errorf("GetNodeStatsAll() on empty returned %d", len(all))
	}
}

// ============================================================
// Rotation Operations
// ============================================================

func TestRecordRotation(t *testing.T) {
	c := setupTestCache(t)

	if err := c.RecordRotation(1, 2, "failure", "success"); err != nil {
		t.Fatal(err)
	}

	rotations, _ := c.GetRecentRotations(10)
	if len(rotations) != 1 {
		t.Fatalf("len(rotations) = %d, want 1", len(rotations))
	}

	r := rotations[0]
	if r.FromNodeID != 1 || r.ToNodeID != 2 {
		t.Errorf("From=%d To=%d, want From=1 To=2", r.FromNodeID, r.ToNodeID)
	}
	if r.Reason != "failure" {
		t.Errorf("Reason = %s, want failure", r.Reason)
	}
	if r.Status != "success" {
		t.Errorf("Status = %s, want success", r.Status)
	}
}

func TestRecordRotation_Multiple(t *testing.T) {
	c := setupTestCache(t)

	c.RecordRotation(1, 2, "failure", "success")
	time.Sleep(10 * time.Millisecond)
	c.RecordRotation(2, 3, "manual", "success")

	rotations, _ := c.GetRecentRotations(10)
	if len(rotations) != 2 {
		t.Fatalf("len(rotations) = %d, want 2", len(rotations))
	}

	// Most recent first
	if rotations[0].FromNodeID != 2 {
		t.Errorf("most recent from = %d, want 2", rotations[0].FromNodeID)
	}
}

func TestGetRecentRotations_Limit(t *testing.T) {
	c := setupTestCache(t)

	for i := 0; i < 10; i++ {
		c.RecordRotation(i, i+1, "test", "success")
	}

	rotations, _ := c.GetRecentRotations(3)
	if len(rotations) != 3 {
		t.Errorf("GetRecentRotations(3) returned %d, want 3", len(rotations))
	}
}

func TestGetRecentRotations_Empty(t *testing.T) {
	c := setupTestCache(t)
	rotations, _ := c.GetRecentRotations(10)
	if len(rotations) != 0 {
		t.Errorf("GetRecentRotations() on empty = %d, want 0", len(rotations))
	}
}

// ============================================================
// PurgeExpiredNodes / DeleteOldNodes
// ============================================================

func TestPurgeExpiredNodes(t *testing.T) {
	c := setupTestCache(t)

	// Node with short TTL
	shortTTL := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless"}
	c.SaveNodes([]*types.Node{shortTTL})

	// Manually set TTL to past
	nodes, _ := c.GetNodes()
	c.db.Model(&types.Node{}).Where("id = ?", nodes[0].ID).Update("ttl_expires_at", time.Now().Add(-1*time.Hour))

	// Node with future TTL
	futureTTL := &types.Node{Server: "5.6.7.8", Port: 80, Protocol: "ss"}
	c.SaveNodes([]*types.Node{futureTTL})

	removed, err := c.PurgeExpiredNodes()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("PurgeExpiredNodes() removed %d, want 1", removed)
	}

	count, _ := c.CountNodes()
	if count != 1 {
		t.Errorf("CountNodes() after purge = %d, want 1", count)
	}
}

func TestDeleteOldNodes(t *testing.T) {
	c := setupTestCache(t)

	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	// Manually expire
	nodes, _ := c.GetNodes()
	c.db.Model(&types.Node{}).Where("id = ?", nodes[0].ID).Update("ttl_expires_at", time.Now().Add(-1*time.Hour))

	removed, err := c.DeleteOldNodes()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("DeleteOldNodes() removed %d, want 1", removed)
	}
}

func TestDeleteOldNodes_NoExpired(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{{Server: "1.2.3.4", Port: 443, Protocol: "vless"}})

	removed, _ := c.DeleteOldNodes()
	if removed != 0 {
		t.Errorf("DeleteOldNodes() removed %d, want 0", removed)
	}
}

// ============================================================
// GetGoodNodes
// ============================================================

func TestGetGoodNodes(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan"},
	})

	nodes, _ := c.GetNodes()
	// node 1: 100% success
	c.UpdateNodeStats(nodes[0].ID, 100, true)
	c.UpdateNodeStats(nodes[0].ID, 100, true)
	// node 2: 50% success
	c.UpdateNodeStats(nodes[1].ID, 100, true)
	c.UpdateNodeStats(nodes[1].ID, 0, false)
	// node 3: 0% success
	c.UpdateNodeStats(nodes[2].ID, 0, false)

	good, err := c.GetGoodNodes(0.75)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 1 {
		t.Errorf("GetGoodNodes(0.75) returned %d, want 1", len(good))
	}
}

// ============================================================
// Concurrent Access
// ============================================================

func TestConcurrentAccess(t *testing.T) {
	c := setupTestCache(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			node := &types.Node{
				Server:   "1.2.3.4",
				Port:     id,
				Protocol: "vless",
			}
			c.SaveNodes([]*types.Node{node})
			c.UpdateNodeStats(0, int64(id), id%2 == 0)
			c.GetNodes()
		}(i)
	}
	wg.Wait()

	count, _ := c.CountNodes()
	if count != 50 {
		t.Errorf("CountNodes() after concurrent writes = %d, want 50", count)
	}
}
