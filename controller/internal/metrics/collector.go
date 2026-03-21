// Package metrics — Prometheus metric collection from services
package metrics

import (
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
)

// ServiceRefs holds references to services for metric collection.
// Avoids importing the api package to prevent import cycles.
type ServiceRefs struct {
	Compute *compute.ComputeService
	Storage *storage.Service
	HA      *ha.Service
}

// CollectFromServices updates gauge metrics from service state.
func CollectFromServices(svc *ServiceRefs) {
	if svc == nil {
		return
	}

	// Collect VM counts by state and backend
	VMsTotal.Reset()
	if svc.Compute != nil {
		vms := svc.Compute.ListVMs()
		counts := make(map[[2]string]float64)
		for _, vm := range vms {
			key := [2]string{vm.State, vm.Backend}
			counts[key]++
		}
		for key, count := range counts {
			VMsTotal.WithLabelValues(key[0], key[1]).Set(count)
		}
	}

	// Collect node count
	if svc.HA != nil {
		nodes := svc.HA.ListNodes()
		NodesTotal.Set(float64(len(nodes)))
	}

	// Collect storage pool metrics
	StoragePoolBytes.Reset()
	if svc.Storage != nil {
		pools := svc.Storage.ListPools()
		for _, pool := range pools {
			StoragePoolBytes.WithLabelValues(pool.Name, "total").Set(float64(pool.TotalBytes))
			StoragePoolBytes.WithLabelValues(pool.Name, "used").Set(float64(pool.UsedBytes))
		}
	}
}
