// Package ha — pluggable HA backend driver interface
package ha

// HADriver is the interface for pluggable HA backends.
type HADriver interface {
	Name() string
	GetClusterStatus() (*ClusterStatus, error)
	ListNodes() ([]*ClusterNode, error)
	FenceNode(nodeName, reason, action string) (*FenceEvent, error)
	ListFenceEvents() ([]FenceEvent, error)
}
