// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/dsx-ai-factory/health-validation/collect"
)

// Link-transition states, the values of LinkTransitionState.
const (
	LinkStateUp   = "up"
	LinkStateDown = "down"
)

// LinkFlapScanSince marks a completed link-transition scan. Its value is the
// window start in Unix microseconds, journald's own time base; transitions
// logged before it are not reported.
var LinkFlapScanSince = collect.NewSignal("infiniband.link_flap.scan.since", "microseconds")

// Raw facts of one mlx5 link transition, one observation per field per
// journal entry. The interface is a value, not the target: it is the netdev
// name the driver logs (enp90s0np0), which neither an InfiniBand device nor
// port target names, and repeated transitions on one interface are distinct
// events that a per-interface target would collapse.
var (
	LinkTransitionInterface = collect.NewSignal("infiniband.link_flap.event.interface", "")
	LinkTransitionState     = collect.NewSignal("infiniband.link_flap.event.state", "")
	// LinkTransitionRealtime is the entry's journal __REALTIME_TIMESTAMP.
	LinkTransitionRealtime = collect.NewSignal("infiniband.link_flap.event.realtime", "microseconds")
)

// LinkFlapSignals returns the scan marker followed by every link-transition
// signal. They are disjoint from [All], the sysfs port signals.
func LinkFlapSignals() collect.Signals {
	return append(collect.Signals{LinkFlapScanSince}, LinkTransitionSignals()...)
}

// LinkTransitionSignals returns the per-transition signals.
func LinkTransitionSignals() collect.Signals {
	return collect.Signals{LinkTransitionInterface, LinkTransitionState, LinkTransitionRealtime}
}

// LinkTransitionParseError reports a line that matched
// [MatchLinkTransition] but did not parse. Detail never contains line content.
type LinkTransitionParseError struct {
	Reason string
	Detail string
}

func (e *LinkTransitionParseError) Error() string {
	return "link transition parse: " + e.Reason + ": " + e.Detail
}

// Parse error reasons. They are stable; Detail wording is not.
const (
	// ReasonMissingInterface is a transition whose prefix is not the
	// driver's "<driver> <device>[ <netdev>]" form.
	ReasonMissingInterface = "missing_interface"
	// ReasonInvalidInterface is a device or netdev name the kernel could
	// not have printed.
	ReasonInvalidInterface = "invalid_interface"
)

// LinkTransition is the raw facts of one mlx5 link-transition line.
type LinkTransition struct {
	// Interface is the netdev name, or the device name when the driver
	// logged none.
	Interface string
	Device    string
	Down      bool
}

// maxDeviceName bounds a device name: PCI addresses and auxiliary device
// names are far shorter, so a longer token is not one the driver printed.
const maxDeviceName = 64

var (
	// transitionMatchRE anchors on the driver at the start of the message,
	// where journald stores the kernel's dev_printk prefix. Another
	// subsystem's line that merely mentions mlx5_core and a link state is
	// not a transition.
	transitionMatchRE = regexp.MustCompile(`(?i)^mlx5_core(?:\.\S+)? .*: Link (?:up|down)\b`)
	// transitionRE is netdev_info's "<driver> <device> <netdev><state>: "
	// prefix, or dev_info's "<driver> <device>: ". <state> is the
	// " (unregistering)"-style suffix netdev_reg_state adds to a netdev that
	// is not registered, which is not part of its name.
	transitionRE = regexp.MustCompile(`(?i)^mlx5_core(?:\.\S+)? (\S+?)(?: (\S+?)(?: \([a-z ]+\))?)?: Link (up|down)\b`)
)

// MatchLinkTransition reports whether message is an mlx5_core link up or
// down line: the driver's prefix followed by ": Link up" or ": Link down".
func MatchLinkTransition(message string) bool { return transitionMatchRE.MatchString(message) }

// ParseLinkTransition extracts the interface and direction of an mlx5 link
// transition, for example
//
//	mlx5_core 0000:5a:00.0 enp90s0np0: Link down
//
// It returns a *LinkTransitionParseError when message matches
// [MatchLinkTransition] but its prefix is not one the driver prints.
func ParseLinkTransition(message string) (LinkTransition, error) {
	m := transitionRE.FindStringSubmatch(message)
	if m == nil {
		return LinkTransition{}, &LinkTransitionParseError{
			Reason: ReasonMissingInterface,
			Detail: `no "<device>[ <interface>]: Link up|down" prefix follows mlx5_core`,
		}
	}
	device, netdev := m[1], m[2]
	if !validDeviceName(device) {
		return LinkTransition{}, &LinkTransitionParseError{Reason: ReasonInvalidInterface, Detail: "the device name is not printable or is too long"}
	}
	iface := device
	if netdev != "" {
		if !validNetdevName(netdev) {
			return LinkTransition{}, &LinkTransitionParseError{Reason: ReasonInvalidInterface, Detail: "the interface name breaks the kernel's netdev naming rules"}
		}
		iface = netdev
	}
	return LinkTransition{Interface: iface, Device: device, Down: strings.EqualFold(m[3], LinkStateDown)}, nil
}

// ValidInterfaceName reports whether name is an interface the parser could
// report: a valid netdev name, or the device name used when the driver
// logged no netdev. Consumers of the interface signal can check a value from
// another producer with it.
func ValidInterfaceName(name string) bool { return validNetdevName(name) || validDeviceName(name) }

// validNetdevName applies the kernel's dev_valid_name: 1 to 15 bytes, not
// "." or "..", and no '/', ':', or whitespace.
func validNetdevName(name string) bool {
	if name == "" || len(name) > 15 || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return r == '/' || r == ':' || unicode.IsSpace(r) || !unicode.IsPrint(r)
	})
}

// validDeviceName accepts printable ASCII without whitespace or '/', which
// the driver core never puts in a device name.
func validDeviceName(name string) bool {
	return name != "" && len(name) <= maxDeviceName && !strings.ContainsFunc(name, func(r rune) bool {
		return r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) || r == '/'
	})
}
