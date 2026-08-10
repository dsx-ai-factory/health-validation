package toolkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NVIDIA/health-validation/catalog/health"
	"github.com/NVIDIA/health-validation/catalog/infiniband"
)

// ibSysfsRoot is where the RDMA subsystem registers its devices.
const ibSysfsRoot = "/sys/class/infiniband"

// ibLinkLayer is the link_layer value of a port running InfiniBand. Cards in
// Ethernet mode still report transport "InfiniBand (0)", so link_layer is the
// field that separates them.
const ibLinkLayer = "InfiniBand"

// InfiniBand returns the host IB backend: it enumerates InfiniBand devices
// from /sys/class/infiniband and emits one [infiniband.InventoryMeasurement].
func InfiniBand() IBBackend {
	return &ibBackend{root: ibSysfsRoot}
}

type ibBackend struct {
	root string
}

func (b *ibBackend) MeasureIB(_ context.Context) ([]health.Measurement, error) {
	devices, err := ibSysfsDevices(b.root)
	if err != nil {
		return []health.Measurement{
			infiniband.ErroredInventory(err, infiniband.InventoryMeasurementSignals...),
		}, nil
	}

	return []health.Measurement{infiniband.InventoryMeasurement{Devices: devices}}, nil
}

// ibSysfsDevices returns the InfiniBand-mode device names under root. A device
// qualifies when any of its ports reports [ibLinkLayer].
func ibSysfsDevices(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("enumerate RDMA devices: %w", err)
	}

	var devices []string
	for _, entry := range entries {
		device := entry.Name()
		isIB, err := hasInfiniBandPort(filepath.Join(root, device, "ports"))
		if err != nil {
			return nil, fmt.Errorf("device %s: %w", device, err)
		}
		if isIB {
			devices = append(devices, device)
		}
	}

	slices.Sort(devices)
	return devices, nil
}

// hasInfiniBandPort reports whether any port under portsDir runs InfiniBand.
// An unreadable link_layer fails collection rather than assuming a mode: a
// device admitted on assumption would report a matching inventory without its
// link layer ever being confirmed.
func hasInfiniBandPort(portsDir string) (bool, error) {
	ports, err := os.ReadDir(portsDir)
	if err != nil {
		return false, fmt.Errorf("enumerate ports: %w", err)
	}

	for _, port := range ports {
		path := filepath.Join(portsDir, port.Name(), "link_layer")
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read %s: %w", path, err)
		}
		if strings.EqualFold(strings.TrimSpace(string(raw)), ibLinkLayer) {
			return true, nil
		}
	}
	return false, nil
}
