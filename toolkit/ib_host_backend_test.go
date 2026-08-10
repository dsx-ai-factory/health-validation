package toolkit

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/NVIDIA/health-validation/catalog/infiniband"
)

// fakeIBSysfs builds a /sys/class/infiniband tree from device name to port
// link layers. An empty link layer omits the attribute file.
func fakeIBSysfs(t *testing.T, devices map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	for device, layers := range devices {
		for i, layer := range layers {
			port := filepath.Join(root, device, "ports", strconv.Itoa(i+1))
			if err := os.MkdirAll(port, 0o755); err != nil {
				t.Fatal(err)
			}
			if layer == "" {
				continue
			}
			if err := os.WriteFile(filepath.Join(port, "link_layer"), []byte(layer+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// The oci-hsg-cs-001-v3 layout: four IB ports and two Ethernet-mode ConnectX.
// Only the IB ones belong to this domain.
func TestIBSysfsDevicesKeepsOnlyInfiniBand(t *testing.T) {
	root := fakeIBSysfs(t, map[string][]string{
		"mlx5_0": {"InfiniBand"},
		"mlx5_1": {"InfiniBand"},
		"mlx5_2": {"Ethernet"},
		"mlx5_3": {"InfiniBand"},
		"mlx5_4": {"InfiniBand"},
		"mlx5_5": {"Ethernet"},
	})

	got, err := ibSysfsDevices(root)
	if err != nil {
		t.Fatalf("ibSysfsDevices: %v", err)
	}
	want := []string{"mlx5_0", "mlx5_1", "mlx5_3", "mlx5_4"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("devices = %v, want %v", got, want)
	}
}

// A dual-port card counts as InfiniBand when either port runs it.
func TestIBSysfsDevicesKeepsMixedPortDevice(t *testing.T) {
	root := fakeIBSysfs(t, map[string][]string{"mlx5_0": {"Ethernet", "InfiniBand"}})

	got, err := ibSysfsDevices(root)
	if err != nil {
		t.Fatalf("ibSysfsDevices: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"mlx5_0"}) {
		t.Errorf("devices = %v, want [mlx5_0]", got)
	}
}

// An unreadable link_layer fails collection: admitting the device would
// report a matching inventory without ever confirming its link layer.
func TestIBSysfsDevicesRejectsUnreadableLinkLayer(t *testing.T) {
	root := fakeIBSysfs(t, map[string][]string{"mlx5_0": {""}})

	_, err := ibSysfsDevices(root)
	if err == nil || !strings.Contains(err.Error(), "link_layer") {
		t.Fatalf("error = %v, want unreadable link_layer failure", err)
	}
}

// A node whose ports all came up in Ethernet mode enumerates cleanly with no
// devices, so the inventory check reports the mismatch against
// expected_devices instead of a collection failure.
func TestIBSysfsDevicesReturnsEmptyWhenNoInfiniBandDevices(t *testing.T) {
	root := fakeIBSysfs(t, map[string][]string{"mlx5_0": {"Ethernet"}})

	got, err := ibSysfsDevices(root)
	if err != nil {
		t.Fatalf("ibSysfsDevices: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("devices = %v, want none", got)
	}
}

// A node with no RDMA subsystem at all fails collection rather than reporting
// an empty inventory.
func TestIBSysfsDevicesRejectsMissingRoot(t *testing.T) {
	if _, err := ibSysfsDevices(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("error = nil, want missing-root failure")
	}
}

// One pass emits the node-wide inventory from a single enumeration.
func TestIBBackendMeasuresInventory(t *testing.T) {
	b := &ibBackend{
		root: fakeIBSysfs(t, map[string][]string{
			"mlx5_0": {"InfiniBand"},
			"mlx5_1": {"InfiniBand"},
		}),
	}

	measurements, err := b.MeasureIB(context.Background())
	if err != nil {
		t.Fatalf("MeasureIB: %v", err)
	}
	if len(measurements) != 1 {
		t.Fatalf("measurements = %d, want 1 (inventory)", len(measurements))
	}

	inventory, ok := measurements[0].(infiniband.InventoryMeasurement)
	if !ok {
		t.Fatalf("measurement 0 = %T, want InventoryMeasurement", measurements[0])
	}
	if !reflect.DeepEqual(inventory.Devices, []string{"mlx5_0", "mlx5_1"}) {
		t.Errorf("devices = %v, want mlx5_0 and mlx5_1", inventory.Devices)
	}
}

// A node with no RDMA subsystem errors the inventory rather than reporting an
// empty enumeration as success.
func TestIBBackendErrorsInventoryOnMissingRoot(t *testing.T) {
	b := &ibBackend{root: filepath.Join(t.TempDir(), "absent")}

	measurements, err := b.MeasureIB(context.Background())
	if err != nil {
		t.Fatalf("MeasureIB: %v", err)
	}
	if len(measurements) != 1 {
		t.Fatalf("measurements = %d, want 1 (errored inventory)", len(measurements))
	}
	inventory, ok := measurements[0].(infiniband.InventoryMeasurement)
	if !ok {
		t.Fatalf("measurement 0 = %T, want InventoryMeasurement", measurements[0])
	}
	if len(inventory.Errors()) != 1 {
		t.Errorf("errors = %v, want the enumeration failure recorded", inventory.Errors())
	}
}
