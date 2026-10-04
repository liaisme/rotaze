// Package cache handles SQLite state persistence for nodes, stats, and state.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"

	"github.com/rotaze/rotaze/pkg/types"
)

// Cache provides SQLite-backed state management.
type Cache struct {
	db *gorm.DB
}

// NewCache opens or creates a SQLite database at the given path.
func NewCache(dbPath string) (*Cache, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}
	// _txlock=immediate takes the write lock at transaction start rather than
	// on first write, so two processes (the daemon and a CLI command) fail
	// fast and retry instead of half-writing. busy_timeout makes SQLite wait
	// for the other process's transaction to finish before erroring.
	dsn := dbPath + "?_txlock=immediate&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	c := &Cache{db: db}
	if err := c.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return c, nil
}

// migrate creates the database schema.
func (c *Cache) migrate() error {
	if err := c.db.AutoMigrate(
		&types.Node{},
		&types.NodeStats{},
		&types.State{},
		&types.Rotation{},
	); err != nil {
		return err
	}
	// The dedup below is a full-table DELETE that runs on every open, which is
	// both slow and a write that can collide with a running daemon. It is only
	// needed once, to make the unique index creatable on databases written
	// before it existed. If the index is already there, there can be no
	// duplicates and the whole step can be skipped.
	var idxCount int64
	if err := c.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_node_identity'").Row().Scan(&idxCount); err != nil {
		return err
	}
	if idxCount > 0 {
		return nil
	}
	if err := c.db.Exec(`DELETE FROM nodes WHERE id NOT IN (
		SELECT MIN(id) FROM nodes GROUP BY server, port, protocol
	)`).Error; err != nil {
		return fmt.Errorf("failed to dedupe nodes: %w", err)
	}
	return c.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_node_identity ON nodes(server, port, protocol)").Error
}

// Close closes the database connection.
func (c *Cache) Close() error {
	sqlDB, err := c.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// SaveNodes saves multiple nodes to the database, deduplicating by (server, port, protocol).
func (c *Cache) SaveNodes(nodes []*types.Node) error {
	if len(nodes) == 0 {
		return nil
	}

	now := time.Now()
	ttl := now.Add(time.Hour)
	for _, node := range nodes {
		node.FetchedAt = now
		node.TTLExpiresAt = ttl
	}

	// Batched upsert using ON CONFLICT on the identity key.
	// SQLite supports this; gorm will translate to INSERT ... ON CONFLICT DO UPDATE.
	err := c.db.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "server"}, {Name: "port"}, {Name: "protocol"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"country",
				"uuid",
				"password",
				"encrypt_method",
				"tls_enabled",
				"tls_sni",
				"transport_type",
				"transport_host",
				"transport_path",
				"alter_id",
				"auth_method",
				"ca_cert",
				"client_cert",
				"client_key",
				"tag",
				"fetched_at",
				"ttl_expires_at",
			}),
		}).CreateInBatches(nodes, 500).Error
	})
	if err != nil {
		return fmt.Errorf("failed to save nodes: %w", err)
	}

	// After upsert, populate IDs on the input nodes so callers can look them up.
	// This is needed by run/daemon code that calls SetCurrentNode(node.ID) after fetch.
	ids := make(map[string]int, len(nodes))
	type rec struct {
		ID       int
		Server   string
		Port     int
		Protocol string
	}
	var rows []rec
	if err := c.db.Table("nodes").Select("id, server, port, protocol").Find(&rows).Error; err == nil {
		for _, r := range rows {
			ids[fmt.Sprintf("%s:%d:%s", r.Server, r.Port, r.Protocol)] = r.ID
		}
	}
	for _, node := range nodes {
		if id, ok := ids[fmt.Sprintf("%s:%d:%s", node.Server, node.Port, node.Protocol)]; ok {
			node.ID = id
		}
	}
	return nil
}

// --- Node Operations ---

// SaveNodes saves multiple nodes to the database, deduplicating by (server, port, protocol).

// GetNodes returns all cached nodes.
func (c *Cache) GetNodes() ([]*types.Node, error) {
	var nodes []*types.Node
	if err := c.db.Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}
	return nodes, nil
}

// GetNodesByCountry returns nodes filtered by country.
func (c *Cache) GetNodesByCountry(country string) ([]*types.Node, error) {
	var nodes []*types.Node
	if err := c.db.Where("country = ?", country).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("failed to get nodes by country: %w", err)
	}
	return nodes, nil
}

// GetNodesByProtocol returns nodes filtered by protocol.
func (c *Cache) GetNodesByProtocol(protocol string) ([]*types.Node, error) {
	var nodes []*types.Node
	if err := c.db.Where("protocol = ?", protocol).Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("failed to get nodes by protocol: %w", err)
	}
	return nodes, nil
}

// GetGoodNodes returns nodes with success rate above the given threshold.
func (c *Cache) GetGoodNodes(minSuccessRate float64) ([]*types.Node, error) {
	var nodes []*types.Node
	if err := c.db.Joins("JOIN node_stats ON node_stats.node_id = nodes.id").
		Where("node_stats.success_rate >= ?", minSuccessRate).
		Find(&nodes).Error; err != nil {
		// Fallback: return all nodes if stats join fails
		return c.GetNodes()
	}
	return nodes, nil
}

// GetNodeByID returns a single node by ID.
func (c *Cache) GetNodeByID(id int) (*types.Node, error) {
	var node types.Node
	if err := c.db.First(&node, id).Error; err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}
	return &node, nil
}

// DeleteOldNodes removes nodes whose TTL has expired.
func (c *Cache) DeleteOldNodes() (int64, error) {
	result := c.db.Where("ttl_expires_at < ?", time.Now()).Delete(&types.Node{})
	return result.RowsAffected, result.Error
}

// CountNodes returns the total number of cached nodes.
func (c *Cache) CountNodes() (int64, error) {
	var count int64
	if err := c.db.Model(&types.Node{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// DeleteNodes removes nodes by their IDs.
func (c *Cache) DeleteNodes(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	return c.db.Where("id IN ?", ids).Delete(&types.Node{}).Error
}

// GetDuplicateGroups returns groups of nodes that share the same (server, port, protocol).
func (c *Cache) GetDuplicateGroups() (map[string][]*types.Node, error) {
	var nodes []*types.Node
	if err := c.db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	groups := make(map[string][]*types.Node)
	for _, n := range nodes {
		key := fmt.Sprintf("%s:%d:%s", n.Server, n.Port, n.Protocol)
		groups[key] = append(groups[key], n)
	}
	return groups, nil
}

// GetNodeCountByProtocol returns count of nodes per protocol.
func (c *Cache) GetNodeCountByProtocol() (map[string]int64, error) {
	var results []struct {
		Protocol string
		Count    int64
	}
	if err := c.db.Model(&types.Node{}).Select("protocol, count(*) as count").Group("protocol").Scan(&results).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, r := range results {
		out[r.Protocol] = r.Count
	}
	return out, nil
}

// GetNodeCountByCountry returns count of nodes per country.
func (c *Cache) GetNodeCountByCountry() (map[string]int64, error) {
	var results []struct {
		Country string
		Count   int64
	}
	if err := c.db.Model(&types.Node{}).Select("country, count(*) as count").Group("country").Order("count DESC").Scan(&results).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int64)
	for _, r := range results {
		out[r.Country] = r.Count
	}
	return out, nil
}

// GetNodeStatsAll returns stats for all nodes.
func (c *Cache) GetNodeStatsAll() ([]*types.NodeStats, error) {
	var stats []*types.NodeStats
	if err := c.db.Find(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

// DeleteNodeStats removes stats for a list of node IDs.
func (c *Cache) DeleteNodeStats(nodeIDs []int) error {
	if len(nodeIDs) == 0 {
		return nil
	}
	return c.db.Where("node_id IN ?", nodeIDs).Delete(&types.NodeStats{}).Error
}

// --- State Operations ---

// SetState sets a key-value pair in the state table.
func (c *Cache) SetState(key, value string) error {
	state := types.State{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
	}
	return c.db.Save(&state).Error
}

// GetState gets a value by key from the state table.
func (c *Cache) GetState(key string) (string, error) {
	var state types.State
	if err := c.db.Where("key = ?", key).First(&state).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return state.Value, nil
}

// SetCurrentNode persists the current node ID.
func (c *Cache) SetCurrentNode(nodeID int) error {
	return c.SetState(types.StateKeyCurrentNodeID, fmt.Sprintf("%d", nodeID))
}

// GetCurrentNode returns the current node ID.
func (c *Cache) GetCurrentNode() (int, error) {
	val, err := c.GetState(types.StateKeyCurrentNodeID)
	if err != nil || val == "" {
		return 0, err
	}
	var id int
	_, err = fmt.Sscanf(val, "%d", &id)
	return id, err
}

// LockNode locks to a specific node (prevents auto-rotation).
func (c *Cache) LockNode(nodeID int) error {
	return c.SetState(types.StateKeyLockedNodeID, fmt.Sprintf("%d", nodeID))
}

// UnlockNode clears the lock.
func (c *Cache) UnlockNode() error {
	return c.SetState(types.StateKeyLockedNodeID, "0")
}

// GetLockedNode returns the locked node ID (0 = unlocked).
func (c *Cache) GetLockedNode() (int, error) {
	val, err := c.GetState(types.StateKeyLockedNodeID)
	if err != nil || val == "" {
		return 0, err
	}
	var id int
	_, err = fmt.Sscanf(val, "%d", &id)
	return id, err
}

// IsNodeLocked returns whether auto-rotation is disabled.
func (c *Cache) IsNodeLocked() (bool, error) {
	id, err := c.GetLockedNode()
	return id != 0, err
}

// --- Stats Operations ---

// UpdateNodeStats records a check result for a node.
func (c *Cache) UpdateNodeStats(nodeID int, latencyMS int64, success bool) error {
	var stats types.NodeStats
	err := c.db.Where("node_id = ?", nodeID).First(&stats).Error
	if err == gorm.ErrRecordNotFound {
		stats = types.NodeStats{
			NodeID:      nodeID,
			TotalChecks: 1,
		}
		if success {
			stats.TotalFailures = 0
		} else {
			stats.TotalFailures = 1
			stats.LastFailure = time.Now()
		}
		stats.LatencyMS = latencyMS
		stats.LastCheck = time.Now()
		if stats.TotalChecks > 0 {
			stats.SuccessRate = float64(stats.TotalChecks-stats.TotalFailures) / float64(stats.TotalChecks)
		}
		return c.db.Create(&stats).Error
	}
	if err != nil {
		return err
	}

	stats.TotalChecks++
	stats.LastCheck = time.Now()
	stats.LatencyMS = latencyMS
	if success {
		stats.ConsecutiveFailures = 0
	} else {
		stats.ConsecutiveFailures++
		stats.LastFailure = time.Now()
		stats.TotalFailures++
	}
	stats.SuccessRate = float64(stats.TotalChecks-stats.TotalFailures) / float64(stats.TotalChecks)

	return c.db.Save(&stats).Error
}

// GetNodeStats returns stats for a specific node.
func (c *Cache) GetNodeStats(nodeID int) (*types.NodeStats, error) {
	var stats types.NodeStats
	if err := c.db.Where("node_id = ?", nodeID).First(&stats).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}

// --- Rotation Operations ---

// RecordRotation records a rotation event.
func (c *Cache) RecordRotation(fromNodeID, toNodeID int, reason, status string) error {
	rotation := types.Rotation{
		FromNodeID: fromNodeID,
		ToNodeID:   toNodeID,
		Reason:     reason,
		Status:     status,
		Timestamp:  time.Now(),
	}
	return c.db.Create(&rotation).Error
}

// GetRecentRotations returns the most recent rotations.
func (c *Cache) GetRecentRotations(limit int) ([]*types.Rotation, error) {
	var rotations []*types.Rotation
	if err := c.db.Order("timestamp DESC").Limit(limit).Find(&rotations).Error; err != nil {
		return nil, err
	}
	return rotations, nil
}

// --- Cleanup ---

// PurgeExpiredNodes removes nodes whose TTL has expired.
func (c *Cache) PurgeExpiredNodes() (int64, error) {
	result := c.db.Where("ttl_expires_at < ?", time.Now()).Delete(&types.Node{})
	return result.RowsAffected, result.Error
}
