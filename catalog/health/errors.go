package health

import "errors"

// ErrCollectionTimeout marks a measurement that failed because collecting
// it exceeded the allotted time, as opposed to a confirmed hardware or
// driver fault. The collection layer wraps it into an errored
// measurement's cause when its watchdog fires; evaluation maps it to a
// retry-collection action distinct from a fault's remediation. Match it
// with [errors.Is].
var ErrCollectionTimeout = errors.New("collection timed out")
