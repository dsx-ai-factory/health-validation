// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const tcpListen = "0A"

var errNoListener = errors.New("no listening socket on the configured address")

// checkOwner, when HV_MCP_SERVE_UID is set, finds the socket listening on serve's
// address in /proc/net/tcp or tcp6 and refuses unless that user owns it. It guards
// against another local user taking the port while serve is down. It reads the network
// namespace this process runs in, so the server must share serve's namespace. Off
// Linux there is no /proc/net, so the check refuses.
func (c *Client) checkOwner() error {
	if c.config.OwnerUID == nil {
		return nil
	}

	if runtime.GOOS != "linux" {
		return &Error{Kind: KindWrongOwner, cause: errors.New("the socket owner check needs Linux")}
	}

	uid, err := listenerOwner(c.config.ProcNet, c.config.Host, c.config.Port)
	if errors.Is(err, errNoListener) {
		return &Error{Kind: KindNotRunning, cause: err}
	}

	if err != nil {
		return &Error{Kind: KindWrongOwner, cause: err}
	}

	if uid != *c.config.OwnerUID {
		return &Error{Kind: KindWrongOwner, cause: fmt.Errorf("the socket belongs to uid %d", uid)}
	}

	return nil
}

// listenerOwner returns the uid owning the listening socket on host:port.
func listenerOwner(procNet, host string, port int) (uint32, error) {
	file, address := "tcp", fmt.Sprintf("0100007F:%04X", port)
	if host == "::1" {
		file, address = "tcp6", fmt.Sprintf("00000000000000000000000001000000:%04X", port)
	}

	table, err := os.Open(filepath.Join(procNet, file))
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", file, err)
	}
	defer func() { _ = table.Close() }()

	scanner := bufio.NewScanner(table)
	scanner.Scan() // the header

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// sl local rem st tx:rx tr:when retrnsmt uid ...
		if len(fields) < 8 || !strings.EqualFold(fields[1], address) || fields[3] != tcpListen {
			continue
		}

		uid, err := strconv.ParseUint(fields[7], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("reading the uid in %s: %w", file, err)
		}

		return uint32(uid), nil
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("reading %s: %w", file, err)
	}

	return 0, errNoListener
}
