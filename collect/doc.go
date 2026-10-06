// Package collect is the shared collection library: source clients,
// sensors, and the observation types they emit.
//
// This module is experimental (v0). Its contracts follow the
// shared-collection-library proposal and may change until per-module
// versioning lands. Nothing in this module applies health judgment; it
// collects and normalizes only.
//
// Sensor observations use typed targets. An InfiniBand port target embeds its
// InfiniBandDeviceRef in device and carries the port number separately;
// kernel-record targets retain the boot ID and sequence number. A sysfs counter
// reporting "N/A (no PMA)" produces a NoValue outcome; a failed file read produces
// a CollectionError. Consumers must inspect the outcome rather than interpreting
// missing values as healthy readings.
package collect
