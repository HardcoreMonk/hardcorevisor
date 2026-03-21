// Package ha — HA Manager for cluster health and fencing
//
// In-memory implementation for dev/test. Manages cluster node
// health monitoring, quorum tracking, and fencing operations.
package ha

import (
	"fmt"
	"sync"
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
	Quorum    bool   `json:"quorum"`
	NodeCount int    `json:"node_count"`
	OnlineCount int  `json:"online_count"`
	Leader    string `json:"leader"`
	Status    string `json:"status"` // healthy, degraded, critical
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
type Service struct {
	mu          sync.RWMutex
	nodes       map[string]*ClusterNode
	fenceEvents []FenceEvent
	fenceNextID int
}

// NewService creates an HA service with default cluster nodes.
func NewService() *Service {
	now := time.Now()
	s := &Service{
		nodes:       make(map[string]*ClusterNode),
		fenceEvents: make([]FenceEvent, 0),
		fenceNextID: 1,
	}

	s.nodes["node-01"] = &ClusterNode{
		Name: "node-01", Status: NodeOnline, LastSeen: now,
		IsLeader: true, VMCount: 2, FenceAgent: "ipmi",
	}
	s.nodes["node-02"] = &ClusterNode{
		Name: "node-02", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 1, FenceAgent: "ipmi",
	}
	s.nodes["node-03"] = &ClusterNode{
		Name: "node-03", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 0, FenceAgent: "ipmi",
	}

	return s
}

// GetClusterStatus returns the aggregated cluster health.
func (s *Service) GetClusterStatus() *ClusterStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cs := &ClusterStatus{NodeCount: len(s.nodes)}
	for _, n := range s.nodes {
		if n.Status == NodeOnline {
			cs.OnlineCount++
		}
		if n.IsLeader {
			cs.Leader = n.Name
		}
	}
	cs.Quorum = cs.OnlineCount > cs.NodeCount/2
	if cs.OnlineCount == cs.NodeCount {
		cs.Status = "healthy"
	} else if cs.Quorum {
		cs.Status = "degraded"
	} else {
		cs.Status = "critical"
	}
	return cs
}

// ListNodes returns all cluster nodes.
func (s *Service) ListNodes() []*ClusterNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*ClusterNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		result = append(result, n)
	}
	return result
}

// FenceNode initiates a fencing operation on a node.
func (s *Service) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeName)
	}

	event := FenceEvent{
		ID:        fmt.Sprintf("fence-%d", s.fenceNextID),
		NodeName:  nodeName,
		Reason:    reason,
		Action:    action,
		Success:   true,
		Timestamp: time.Now(),
	}
	s.fenceNextID++
	s.fenceEvents = append(s.fenceEvents, event)

	node.Status = NodeFenced
	return &event, nil
}

// ListFenceEvents returns fence history.
func (s *Service) ListFenceEvents() []FenceEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]FenceEvent, len(s.fenceEvents))
	copy(result, s.fenceEvents)
	return result
}
