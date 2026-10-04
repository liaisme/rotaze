// Package types defines shared data types used across the Rotaze project.
package types

import "time"

// Node represents a tunnel server configuration.
type Node struct {
	ID            int    `json:"id" gorm:"primaryKey"`
	Server        string `json:"server"`
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"` // vless, ss, trojan, vmess, hy2, ovpn
	Country       string `json:"country"`
	UUID          string `json:"uuid,omitempty"`           // VLESS
	Password      string `json:"password,omitempty"`       // SS, Trojan, Hy2, OpenVPN
	EncryptMethod string `json:"encrypt_method,omitempty"` // SS
	TLSEnabled    bool   `json:"tls_enabled"`
	TLSSNI        string `json:"tls_sni,omitempty"`
	TransportType string `json:"transport_type,omitempty"` // ws, grpc, tcp
	TransportHost string `json:"transport_host,omitempty"`
	TransportPath string `json:"transport_path,omitempty"`
	AlterID       int    `json:"alter_id,omitempty"` // VMess
	// OpenVPN: HMAC auth algorithm (SHA1/SHA256) from the ovpn "auth" line.
	// Router config (vpn/vpn login) always uses SHA1; servers that require
	// a different digest fall back to SHA1 in the builder if empty.
	AuthMethod string `json:"auth_method,omitempty"`
	// OpenVPN client certificate material, stored PEM-encoded.
	CACert       string    `json:"ca_cert,omitempty"`
	ClientCert   string    `json:"client_cert,omitempty"`
	ClientKey    string    `json:"client_key,omitempty"`
	Tag          string    `json:"tag"`
	CreatedAt    time.Time `json:"created_at"`
	FetchedAt    time.Time `json:"fetched_at"`
	TTLExpiresAt time.Time `json:"ttl_expires_at"`
}

// NodeStats represents performance metrics for a node.
type NodeStats struct {
	NodeID              int       `json:"node_id" gorm:"primaryKey"`
	LatencyMS           int64     `json:"latency_ms"`
	SuccessRate         float64   `json:"success_rate"` // 0.0 - 1.0
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastCheck           time.Time `json:"last_check"`
	LastFailure         time.Time `json:"last_failure"`
	TotalChecks         int       `json:"total_checks"`
	TotalFailures       int       `json:"total_failures"`
}

// State represents the current operation state.
type State struct {
	Key       string    `json:"key" gorm:"primaryKey"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Rotation represents a rotation history entry.
type Rotation struct {
	ID         int       `json:"id" gorm:"primaryKey"`
	FromNodeID int       `json:"from_node_id"`
	ToNodeID   int       `json:"to_node_id"`
	Reason     string    `json:"reason"` // failure, manual, refresh, startup
	Status     string    `json:"status"` // success, failed
	Timestamp  time.Time `json:"timestamp"`
}

// NodeCategory represents the quality category of a node.
type NodeCategory string

const (
	NodeCategoryGood NodeCategory = "good"
	NodeCategoryOkay NodeCategory = "okay"
	NodeCategoryBad  NodeCategory = "bad"
)

// String returns the string representation of NodeCategory.
func (c NodeCategory) String() string {
	return string(c)
}

// CategorizeNode determines the quality category based on success rate.
func CategorizeNode(successRate float64) NodeCategory {
	switch {
	case successRate > 0.75:
		return NodeCategoryGood
	case successRate > 0.50:
		return NodeCategoryOkay
	default:
		return NodeCategoryBad
	}
}

// State keys used in the state table.
const (
	StateKeyCurrentNodeID   = "current_node_id"
	StateKeyLockedNodeID    = "locked_node_id"
	StateKeyLastCheckTime   = "last_check_time"
	StateKeyLastCheckStatus = "last_check_status"
)
