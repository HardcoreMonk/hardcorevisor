// Package ha — HA Manager for cluster health and fencing
//
// Supports pluggable drivers (memory, etcd). Default is in-memory
// for dev/test. Manages cluster node health monitoring, quorum
// tracking, and fencing operations.
package ha

import (
	"time"
)

// ── Types ────────────────────────────────────────────

// NodeStatus represents the health state of a cluster node
type NodeStatus string

const (
	NodeOnline  NodeStatus = "online"
	NodeOffline NodeStatus = "offline"
	NodeFenced  NodeStatus = "fenced"
)

// ClusterNode represents a node in the HA cluster
type ClusterNode struct {
	Name       string     `json:"name"`
	Status     NodeStatus `json:"status"`
	LastSeen   time.Time  `json:"last_seen"`
	IsLeader   bool       `json:"is_leader"`
	VMCount    int        `json:"vm_count"`
	FenceAgent string     `json:"fence_agent"` // ipmi, stonith, etc.
}

// ClusterStatus summarizes the HA cluster state
type ClusterStatus struct {
	Quorum      bool   `json:"quorum"`
	NodeCount   int    `json:"node_count"`
	OnlineCount int    `json:"online_count"`
	Leader      string `json:"leader"`
	Status      string `json:"status"` // healthy, degraded, critical
}

// FenceEvent records a fencing operation
type FenceEvent struct {
	ID        string    `json:"id"`
	NodeName  string    `json:"node_name"`
	Reason    string    `json:"reason"`
	Action    string    `json:"action"` // reboot, off, on
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
}

// ── Service ──────────────────────────────────────────

// Service manages HA cluster operations.
// It delegates to an HADriver for the actual backend operations.
type Service struct {
	driver HADriver
}

// NewService creates an HA service with the default in-memory driver.
func NewService() *Service {
	return NewServiceWithDriver(newMemoryDriver())
}

// NewServiceWithDriver creates an HA service with the specified driver.
func NewServiceWithDriver(driver HADriver) *Service {
	return &Service{driver: driver}
}

// GetClusterStatus returns the aggregated cluster health.
func (s *Service) GetClusterStatus() *ClusterStatus {
	cs, err := s.driver.GetClusterStatus()
	if err != nil {
		return &ClusterStatus{Status: "unknown"}
	}
	return cs
}

// ListNodes returns all cluster nodes.
func (s *Service) ListNodes() []*ClusterNode {
	nodes, err := s.driver.ListNodes()
	if err != nil {
		return nil
	}
	return nodes
}

// FenceNode initiates a fencing operation on a node.
func (s *Service) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	return s.driver.FenceNode(nodeName, reason, action)
}

// ListFenceEvents returns fence history.
func (s *Service) ListFenceEvents() []FenceEvent {
	events, err := s.driver.ListFenceEvents()
	if err != nil {
		return nil
	}
	return events
}
