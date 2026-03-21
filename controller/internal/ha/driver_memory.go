// Package ha — in-memory HA driver for dev/test
package ha

import (
	"fmt"
	"sync"
	"time"
)

// MemoryDriver implements HADriver with in-memory state.
type MemoryDriver struct {
	mu          sync.RWMutex
	nodes       map[string]*ClusterNode
	fenceEvents []FenceEvent
	fenceNextID int
}

// newMemoryDriver creates a MemoryDriver with default cluster nodes.
func newMemoryDriver() *MemoryDriver {
	now := time.Now()
	d := &MemoryDriver{
		nodes:       make(map[string]*ClusterNode),
		fenceEvents: make([]FenceEvent, 0),
		fenceNextID: 1,
	}

	d.nodes["node-01"] = &ClusterNode{
		Name: "node-01", Status: NodeOnline, LastSeen: now,
		IsLeader: true, VMCount: 2, FenceAgent: "ipmi",
	}
	d.nodes["node-02"] = &ClusterNode{
		Name: "node-02", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 1, FenceAgent: "ipmi",
	}
	d.nodes["node-03"] = &ClusterNode{
		Name: "node-03", Status: NodeOnline, LastSeen: now,
		IsLeader: false, VMCount: 0, FenceAgent: "ipmi",
	}

	return d
}

// Name returns the driver name.
func (d *MemoryDriver) Name() string { return "memory" }

// GetClusterStatus returns the aggregated cluster health.
func (d *MemoryDriver) GetClusterStatus() (*ClusterStatus, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	cs := &ClusterStatus{NodeCount: len(d.nodes)}
	for _, n := range d.nodes {
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
	return cs, nil
}

// ListNodes returns all cluster nodes.
func (d *MemoryDriver) ListNodes() ([]*ClusterNode, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*ClusterNode, 0, len(d.nodes))
	for _, n := range d.nodes {
		result = append(result, n)
	}
	return result, nil
}

// FenceNode initiates a fencing operation on a node.
func (d *MemoryDriver) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	node, ok := d.nodes[nodeName]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", nodeName)
	}

	event := FenceEvent{
		ID:        fmt.Sprintf("fence-%d", d.fenceNextID),
		NodeName:  nodeName,
		Reason:    reason,
		Action:    action,
		Success:   true,
		Timestamp: time.Now(),
	}
	d.fenceNextID++
	d.fenceEvents = append(d.fenceEvents, event)

	node.Status = NodeFenced
	return &event, nil
}

// ListFenceEvents returns fence history.
func (d *MemoryDriver) ListFenceEvents() ([]FenceEvent, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]FenceEvent, len(d.fenceEvents))
	copy(result, d.fenceEvents)
	return result, nil
}
