// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	tcpEstablished = "01"
	tcpListen      = "0A"
)

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

// checkPeer, when HV_MCP_SERVE_UID is set, refuses a dialled connection unless the
// configured user owns serve's end of it. checkOwner alone leaves a gap between the check
// and the dial, in which another local user could take the port; this closes it, as the
// request only goes over a connection that passed. An accepted socket keeps the owner of
// the listener that accepted it.
func (c *Client) checkPeer(conn net.Conn) error {
	if c.config.OwnerUID == nil {
		return nil
	}

	if runtime.GOOS != "linux" {
		return &Error{Kind: KindWrongOwner, cause: errors.New("the socket owner check needs Linux")}
	}

	local, localOK := conn.LocalAddr().(*net.TCPAddr)
	remote, remoteOK := conn.RemoteAddr().(*net.TCPAddr)
	if !localOK || !remoteOK {
		return &Error{Kind: KindWrongOwner, cause: errors.New("not a TCP connection")}
	}

	uid, err := peerOwner(c.config.ProcNet, remote.AddrPort(), local.AddrPort())
	if err != nil {
		return &Error{Kind: KindWrongOwner, cause: err}
	}

	if uid != *c.config.OwnerUID {
		return &Error{Kind: KindWrongOwner, cause: fmt.Errorf("the connection's far end belongs to uid %d", uid)}
	}

	return nil
}

// listenerOwner returns the uid owning the listening socket on host:port.
func listenerOwner(procNet, host string, port int) (uint32, error) {
	ip := netip.MustParseAddr("127.0.0.1")
	if host == "::1" {
		ip = netip.IPv6Loopback()
	}

	file, address := procAddress(netip.AddrPortFrom(ip, uint16(port)))

	return socketOwner(procNet, file, address, "", tcpListen, errNoListener)
}

// peerOwner returns the uid owning serve's end of an established connection: the row
// whose local address is serve's and whose remote address is this client's.
func peerOwner(procNet string, server, client netip.AddrPort) (uint32, error) {
	file, local := procAddress(server)
	_, remote := procAddress(client)

	return socketOwner(procNet, file, local, remote, tcpEstablished,
		errors.New("no established socket for the connection"))
}

// procAddress writes an address the way /proc/net/tcp and tcp6 do: each 32-bit word of
// the IP in host (little-endian) byte order, then the port, all in hex.
func procAddress(address netip.AddrPort) (file, text string) {
	ip := address.Addr().Unmap()
	raw := ip.AsSlice()

	file = "tcp"
	if ip.Is6() {
		file = "tcp6"
	}

	for word := 0; word+4 <= len(raw); word += 4 {
		raw[word], raw[word+1], raw[word+2], raw[word+3] = raw[word+3], raw[word+2], raw[word+1], raw[word]
	}

	return file, fmt.Sprintf("%s:%04X", strings.ToUpper(hex.EncodeToString(raw)), address.Port())
}

// socketOwner returns the uid in the first row of procNet/file with the local address,
// the remote address (any when empty) and the state given.
func socketOwner(procNet, file, local, remote, state string, notFound error) (uint32, error) {
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
		if len(fields) < 8 || !strings.EqualFold(fields[1], local) || fields[3] != state ||
			(remote != "" && !strings.EqualFold(fields[2], remote)) {
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

	return 0, notFound
}
