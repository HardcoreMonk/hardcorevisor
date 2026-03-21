// Package ha — etcd-backed HA driver with leader election
package ha

import (
	"context"
	"encoding/json"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// EtcdDriver implements HADriver backed by etcd for distributed HA state.
type EtcdDriver struct {
	MemoryDriver // embed for base behavior
	store        store.Store
	nodeName     string
}

// NewEtcdDriver creates an etcd-backed HA driver.
// It registers the current node in etcd and embeds a MemoryDriver for fallback.
func NewEtcdDriver(kvStore store.Store, nodeName string) *EtcdDriver {
	d := &EtcdDriver{
		store:    kvStore,
		nodeName: nodeName,
	}
	d.MemoryDriver = *newMemoryDriver()

	// Register this node in etcd
	ctx := context.Background()
	_ = kvStore.Put(ctx, "ha/nodes/"+nodeName, &ClusterNode{
		Name:       nodeName,
		Status:     NodeOnline,
		LastSeen:   time.Now(),
		FenceAgent: "ipmi",
	})

	return d
}

// Name returns the driver name.
func (d *EtcdDriver) Name() string { return "etcd" }

// GetClusterStatus reads node states from etcd and computes quorum.
func (d *EtcdDriver) GetClusterStatus() (*ClusterStatus, error) {
	nodes, err := d.ListNodes()
	if err != nil {
		return nil, err
	}

	cs := &ClusterStatus{NodeCount: len(nodes)}
	for _, n := range nodes {
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

// ListNodes reads from etcd "ha/nodes/" prefix.
// Falls back to the embedded MemoryDriver if etcd is unavailable.
func (d *EtcdDriver) ListNodes() ([]*ClusterNode, error) {
	kvs, err := d.store.List(context.Background(), "ha/nodes/")
	if err != nil {
		// Fall back to memory driver
		return d.MemoryDriver.ListNodes()
	}

	// If no nodes stored in etcd yet, fall back to memory driver
	if len(kvs) == 0 {
		return d.MemoryDriver.ListNodes()
	}

	// Parse nodes from stored JSON
	nodes := make([]*ClusterNode, 0, len(kvs))
	for _, kv := range kvs {
		var node ClusterNode
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			continue // skip malformed entries
		}
		nodes = append(nodes, &node)
	}

	if len(nodes) == 0 {
		return d.MemoryDriver.ListNodes()
	}

	return nodes, nil
}

// FenceNode fences a node and persists the fence event to etcd.
func (d *EtcdDriver) FenceNode(nodeName, reason, action string) (*FenceEvent, error) {
	event, err := d.MemoryDriver.FenceNode(nodeName, reason, action)
	if err != nil {
		return nil, err
	}

	// Persist fence event to etcd
	ctx := context.Background()
	_ = d.store.Put(ctx, "ha/fence-events/"+event.ID, event)

	// Update node status in etcd
	_ = d.store.Put(ctx, "ha/nodes/"+nodeName, &ClusterNode{
		Name:       nodeName,
		Status:     NodeFenced,
		LastSeen:   time.Now(),
		FenceAgent: "ipmi",
	})

	return event, nil
}
