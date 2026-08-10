// Package infiniband defines the measurement types for InfiniBand and RDMA
// device health, collected by a sensor and evaluated by the catalog against
// InfiniBand check definitions.
package infiniband

import "github.com/NVIDIA/health-validation/catalog/health"

// InfiniBand signal field names. These are the keys used in CEL rule
// expressions and must match the RequiredSignals declared in InfiniBand check
// definitions. SignalDevices is node-wide.
const (
	SignalDevices = "devices"
)

// InventoryMeasurementSignals lists every signal an InventoryMeasurement
// carries. Sensors pass it as the failedSignals argument when enumeration
// failed outright.
var InventoryMeasurementSignals = []string{
	SignalDevices,
}

// InventoryMeasurement captures the InfiniBand device names a node
// enumerates. Sensors emit exactly one per collection pass.
type InventoryMeasurement struct {
	Devices []string `json:"devices"`
	errors  []health.ErroredMeasurement
}

// ErroredInventory returns an inventory measurement whose device enumeration
// failed.
func ErroredInventory(err error, failedSignals ...string) InventoryMeasurement {
	m := InventoryMeasurement{}
	m.AddError(err, failedSignals...)
	return m
}

// AddError appends a collection failure to the measurement.
func (m *InventoryMeasurement) AddError(err error, failedSignals ...string) {
	m.errors = append(m.errors, health.NewErroredMeasurement(err, failedSignals...))
}

// Category implements [health.Measurement].
func (InventoryMeasurement) Category() health.Category { return health.InfiniBand }

// Errors implements [health.Measurement].
func (m InventoryMeasurement) Errors() []health.ErroredMeasurement { return m.errors }
