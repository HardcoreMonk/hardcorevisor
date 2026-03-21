package ha

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
)

// Heartbeat manages periodic node registration in etcd.
type Heartbeat struct {
	store    store.Store
	nodeName string
	interval time.Duration
	cancel   context.CancelFunc
}

// NewHeartbeat creates a heartbeat manager.
func NewHeartbeat(kvStore store.Store, nodeName string, interval time.Duration) *Heartbeat {
	return &Heartbeat{
		store:    kvStore,
		nodeName: nodeName,
		interval: interval,
	}
}

// Start begins periodic heartbeat registration.
func (h *Heartbeat) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() {
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()

		// Initial registration
		h.register(ctx)

		for {
			select {
			case <-ticker.C:
				h.register(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()

	slog.Info("heartbeat started", "node", h.nodeName, "interval", h.interval)
}

// Stop halts the heartbeat.
func (h *Heartbeat) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
}

func (h *Heartbeat) register(ctx context.Context) {
	node := &ClusterNode{
		Name:     h.nodeName,
		Status:   NodeOnline,
		LastSeen: time.Now(),
	}

	if err := h.store.Put(ctx, "ha/nodes/"+h.nodeName, node); err != nil {
		slog.Warn("heartbeat registration failed", "node", h.nodeName, "error", err)
	}
}

// GetNodeName returns the hostname or HCV_NODE_NAME env var.
func GetNodeName() string {
	if name := os.Getenv("HCV_NODE_NAME"); name != "" {
		return name
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
