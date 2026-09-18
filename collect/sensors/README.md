# Collection sensors

Sensors emit raw observations through `collect.Sensor`; they do not store
history or decide health. `agentkit/examples/scan-one-off` and
`agentkit/examples/server-simple-collect` register the built-in sensors.

| Sensor ID | Source | Coverage |
| --- | --- | --- |
| `gpu.nvml` | NVML instance | Core/HBM temperature, thermal margin, four temperature thresholds, power draw, enforced/requested power limits |
| `gpu.dcgm` | Standalone DCGM source | Configured canonical GPU fields |
| `infiniband.sysfs` | `/sys/class/infiniband` | HCA inventory, port state, physical state, link layer, link rate, GPUd's standard and hardware counter fields |
| `kernel.kmsg` | `/dev/kmsg` | Regex-selected message body and priority, identified by boot ID and sequence number |

Create a `collect.Cycle` and pass it with `collect.WithCycle` to each measurement.
`collect.WithSignalID` selects fields and rejects unknown IDs. The NVML, InfiniBand, and kernel sensors use the cycle timeout and reject overlapping
reads while a timed-out read is still running. NVML calls without `WithCycle`
retain the existing synchronous API.

## NVML power and temperature

The NVML sensor uses the same power signals as DCGM, expressed in watts as
`double` values; it converts NVML milliwatts without integer truncation. GPUd's
three power queries correspond to draw, enforced limit and requested limit.
Temperature values remain signed integers in Celsius, including negative thermal
margin. Unsupported reads produce collection errors rather than zero sentinels.
GPU loss or reset aborts remaining queries for that GPU across both families. Derived
usage percentages and thermal-health thresholds remain consumer policy.

NVML `Instance.Read` guards native queries process-wide, as DCGM does. `Refresh`
and `Shutdown` use the same guard. Shutdown returns `ErrInUse` while a read is
still running; callers can retry after it finishes. The source owns the native
library lifecycle; a timeout cannot forcibly cancel a native call.

## InfiniBand

Construct `infiniband.New(source)` with a
`collect/source/infiniband.New(files, excludedDevices...)` source. Nil uses the
host sysfs; `os.DirFS` can point at a mounted host sysfs, and `fstest.MapFS` supports
tests. Reuse the source across cycles. It re-enumerates ports each time, reads
only selected fields, and remembers files returning `EINVAL` to avoid repeated
ACCESS_REG errors on restricted PFs. Recreate the source after reconfiguration
to retry those files. Excluded HCA names are not read.

Entities are node-local `infiniband_port` identities such as `mlx5_0/1`. Data
counters are converted from four-octet units to bytes; link capacity is converted
from Gb/sec to bytes/second. Other counters retain their native units (`lifespan`
is milliseconds; `port_xmit_wait` counts hardware ticks). Missing fields,
permissions, unsupported values, parse failures and signed-wire overflow become
per-field collection errors. A failed field does not discard healthy siblings.
Counters remain cumulative: deltas, resets, thresholds, flap history and SQLite
storage are outside the sensor. Firmware version, board ID and HCA type use `infiniband_device` entities.
Pre-4.5 kernel `counters_ext` is not collected.

## Kernel messages

`collect/source/kmsg.Parse` is a platform-independent parser for one kernel
record, using an explicit boot-time origin. It retains priority, sequence number,
microsecond timestamp and multiline body. Linux `ReadAll` reads the ring buffer
nonblocking, continuing after overwritten records (`EPIPE`) and empty reads as
in GPUd. It reports overwrite loss after draining the remaining records and
preserves partial results on errors. Boot time comes from `/proc/stat` btime.
The reader/parser adapts GPUd at commit
`9606bb8f6813fdd944cc9a349566e35075917805`. It retains partial results and reports
overwrite loss; GPUd suppresses that loss and discards results on other errors.
Timestamp overflow is rejected. GPUd's container-specific boot-time fallback,
stateful event matchers and deduplication are not included.
Other platforms return unsupported. Reading requires access to `/dev/kmsg` and
the boot ID in procfs.

`collect/sensors/kmsg.New(nil)` uses `ReadAll` and vendor-neutral default
patterns for kernel panics, OOM, hung tasks, CPU lockups, and file-max exhaustion.
GPU, networking, storage, or application patterns are supplied by consumers.
These patterns select evidence, not health verdicts, and do not cover every
kernel failure.

```go
sensor, err := kmsg.New(nil) // defaults
custom := kmsg.Pattern{SignalID: "my_driver.failure", Regex: `my_driver: .*failed`}
sensor, err = kmsg.New(nil, custom) // replace defaults
patterns := append(kmsg.DefaultPatterns(), custom)
sensor, err = kmsg.New(nil, patterns...) // extend defaults
sensor, err = kmsg.New(nil, kmsg.Pattern{SignalID: "kernel.all", Regex: `(?s).*`})
```

Handle constructor errors before registering the sensor. Patterns compile once;
invalid or blank patterns and empty, duplicate, or reserved signal IDs are
rejected. `kernel.message` and `kernel.priority` are reserved for raw evidence.
Matching is case-sensitive unless a pattern opts into `(?i)`. No patterns means
defaults. IDs must retain their meaning when configurations change.

Each matching pattern emits its exact regex as a string value under its signal
ID, on the same record entity and timestamp as `kernel.message` and
`kernel.priority`. For example, a `my_driver.failure` observation carries
`my_driver: .*failed`, while `kernel.message` carries the original log text.
Multiple matching patterns emit separate signals; the raw message and priority
are emitted only once. Unmatched patterns emit no observation, not a healthy
verdict. `Signals()` advertises raw and configured pattern signals, and
`collect.WithSignalID("my_driver.failure")` selects just that match signal.
Selecting raw signals returns records matching any configured pattern.

An injected read function can provide fixtures or a consumer-owned snapshot
with a nonempty boot ID and valid timestamp on every record. Observations retain
the original body, priority, timestamp, and `kernel_record` identity
`<boot-id>/<sequence>`. Repeated bodies with different sequence numbers remain
separate. Read and parse errors remain visible regardless of the filters.
The sensor owns no reader lifecycle, database, or health verdicts.
Consumers can reuse the source's typed messages for stateful or domain-specific
parsing. Register the sensor only when kernel-log exposure is intended.

### Shared server stream

`server-simple-collect` starts one `kmsg.NewStream` before serving requests and
closes it when serving ends. Startup fails if the kernel reader cannot be opened;
this example requires Linux and permission to read the host kernel log.
`--kmsg-capacity` bounds retention (default 4096 records).

```go
stream, err := kmsgsource.NewStream(4096)
// Handle err before using stream.
defer stream.Close()
sensor, err := kmsgsensor.New(stream.Read, patterns...)
```

One continuous reader handles the existing kernel ring and future records.
All patterns share non-destructive snapshots of its bounded in-memory window;
requests do not open the device or compete for channel messages. Separate sensor
instances can also share `stream.Read`, though registering one sensor with all
patterns avoids duplicate raw-message signals. Each snapshot is independent;
snapshots taken at different times may contain different records.

Records are ordered oldest first. The oldest records are evicted at capacity;
this is a recent-record window, not complete boot history or persistent storage.
Snapshots may be empty while the reader starts. Repeated bodies are retained.
Overwrites in the kernel ring remain visible as sticky `EPIPE` errors alongside
retained records. Terminal reader errors remain visible; the stream does not
silently restart. `Close` interrupts reading and waits for the goroutine to exit.
The one-off example continues to use the snapshot reader.
