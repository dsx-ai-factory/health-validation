// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/cpu"
	"github.com/dsx-ai-factory/health-validation/proto/verdictjson"
	"github.com/dsx-ai-factory/health-validation/toolkit/verdictproto"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestConfigOnlyAcceptsLoopback(t *testing.T) {
	config, err := ConfigFromEnv(env(nil))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:8080", config.Address())
	assert.Equal(t, DefaultEvaluateTimeout, config.EvaluateTimeout)
	assert.Nil(t, config.OwnerUID)

	six, err := ConfigFromEnv(env(map[string]string{EnvHost: "::1", EnvPort: "9000", EnvOwnerUID: "0"}))
	require.NoError(t, err)
	assert.Equal(t, "[::1]:9000", six.Address())
	require.NotNil(t, six.OwnerUID)

	for name, values := range map[string]map[string]string{
		"a hostname":         {EnvHost: "localhost"},
		"another address":    {EnvHost: "10.0.0.1"},
		"any address":        {EnvHost: "0.0.0.0"},
		"a URL":              {EnvHost: "http://127.0.0.1"},
		"a low port":         {EnvPort: "80"},
		"a high port":        {EnvPort: "65536"},
		"not a port":         {EnvPort: "8080/evil"},
		"a negative uid":     {EnvOwnerUID: "-1"},
		"a short timeout":    {EnvEvaluateTimeout: "9"},
		"a long timeout":     {EnvEvaluateTimeout: "901"},
		"a wrapping timeout": {EnvEvaluateTimeout: "18446744083"},
		"timeout not a nr":   {EnvEvaluateTimeout: "1m"},
	} {
		_, err := ConfigFromEnv(env(values))
		assert.Error(t, err, name)
	}
}

func serve(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)

	number, err := strconv.Atoi(port)
	require.NoError(t, err)

	client, err := New(Config{Host: "127.0.0.1", Port: number, EvaluateTimeout: MinEvaluateTimeout})
	require.NoError(t, err)

	return client
}

func goodVerdict(t *testing.T) []byte {
	t.Helper()

	verdict, err := catalog.New(catalog.WithArchitecture("unknown")).EvaluateAll(
		cpu.CPUMeasurement{PossibleCPUCount: 4, OnlineCPUCount: 4})
	require.NoError(t, err)

	wire, err := verdictproto.ToProto(verdict)
	require.NoError(t, err)

	raw, err := verdictjson.Marshal(wire)
	require.NoError(t, err)

	return raw
}

// net/http never proxies a loopback host, so setting HTTP_PROXY proves nothing here.
// The transport itself must have no proxy function.
func TestProxySettingsAreIgnored(t *testing.T) {
	client, err := New(Config{Host: DefaultHost, Port: DefaultPort, EvaluateTimeout: DefaultEvaluateTimeout})
	require.NoError(t, err)

	transport, ok := client.http.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, transport.Proxy, "a proxy function would read HTTP_PROXY")
}

func TestRedirectsAreRefused(t *testing.T) {
	followed := false
	client := serve(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/elsewhere" {
			followed = true
			return
		}

		http.Redirect(writer, request, "/elsewhere", http.StatusTemporaryRedirect)
	})

	_, err := client.Evaluate(t.Context(), EvaluateRequest{})
	assert.Equal(t, KindRedirect, KindOf(err))
	assert.False(t, followed)
}

func TestBodiesAreCapped(t *testing.T) {
	// The documented cap: 4 MiB. Literal numbers, so changing the cap fails here.
	assert.Equal(t, 4<<20, MaxBodyBytes)

	for size, tooLarge := range map[int]bool{4<<20 + 1: true, 4<<20 - 64: false} {
		client := serve(t, func(writer http.ResponseWriter, _ *http.Request) {
			prefix := `{"schema_version":"0.7","x":"`
			_, _ = writer.Write([]byte(prefix))
			_, _ = writer.Write([]byte(strings.Repeat("a", size-len(prefix)-2)))
			_, _ = writer.Write([]byte(`"}`))
		})

		_, err := client.Evaluate(t.Context(), EvaluateRequest{})
		assert.Equal(t, tooLarge, KindOf(err) == KindTooLarge, "a %d-byte body", size)
	}
}

func TestErrorsHideTheirDetail(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	client, err := New(Config{Host: "127.0.0.1", Port: port, EvaluateTimeout: MinEvaluateTimeout})
	require.NoError(t, err)

	_, err = client.Liveness(t.Context())
	require.Error(t, err)
	assert.Equal(t, KindNotRunning, KindOf(err))
	assert.NotContains(t, err.Error(), "127.0.0.1")

	var clientError *Error
	require.ErrorAs(t, err, &clientError)
	assert.Contains(t, clientError.Detail(), "127.0.0.1", "the detail is kept for the log")
}

func TestTimeoutIsItsOwnKind(t *testing.T) {
	done := make(chan struct{})
	client := serve(t, func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-done:
		}
	})
	// Cleanups run last first, so the handler returns before the server closes.
	t.Cleanup(func() { close(done) })

	client.config.EvaluateTimeout = 50 * time.Millisecond

	_, err := client.Evaluate(t.Context(), EvaluateRequest{})
	assert.Equal(t, KindTimeout, KindOf(err))
}

func TestLiveness(t *testing.T) {
	for body, want := range map[string]string{
		`{"status":"ok"}`: "ok",
		`{"status":"unhealthy","stuck_calls":[{"source":"nvml","since":"2026-11-26T10:00:00Z"}]}`: "unhealthy",
	} {
		client := serve(t, func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(body)) })

		liveness, err := client.Liveness(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, liveness.Status)
	}

	odd := serve(t, func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(`{"status":"fine"}`)) })
	_, err := odd.Liveness(t.Context())
	assert.Equal(t, KindBadResponse, KindOf(err))
}

func TestListenerOwner(t *testing.T) {
	directory := t.TempDir()
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	row := func(address string, state string, uid int) string {
		return fmt.Sprintf("   0: %s 00000000:0000 %s 00000000:00000000 00:00000000 00000000 %5d        0 1 1 0\n",
			address, state, uid)
	}

	require.NoError(t, os.WriteFile(filepath.Join(directory, "tcp"), []byte(header+
		row("0100007F:1F90", "01", 7)+ // an established connection on the port, not the listener
		row("0100007F:1F90", "0A", 1000)+
		row("0100007F:1F91", "0A", 0)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "tcp6"), []byte(header+
		row("00000000000000000000000001000000:1F90", "0A", 2000)), 0o600))

	uid, err := listenerOwner(directory, "127.0.0.1", 8080)
	require.NoError(t, err)
	assert.Equal(t, uint32(1000), uid)

	uid, err = listenerOwner(directory, "::1", 8080)
	require.NoError(t, err)
	assert.Equal(t, uint32(2000), uid)

	_, err = listenerOwner(directory, "127.0.0.1", 9000)
	assert.True(t, errors.Is(err, errNoListener))

	if runtime.GOOS != "linux" {
		return
	}

	owner := uint32(1000)
	client, err := New(Config{
		Host: "127.0.0.1", Port: 8081, EvaluateTimeout: MinEvaluateTimeout,
		OwnerUID: &owner, ProcNet: directory,
	})
	require.NoError(t, err)

	_, err = client.Evaluate(t.Context(), EvaluateRequest{})
	assert.Equal(t, KindWrongOwner, KindOf(err), "port 8081 is owned by uid 0")
}

func TestProcAddress(t *testing.T) {
	file, text := procAddress(netip.MustParseAddrPort("127.0.0.1:8080"))
	assert.Equal(t, "tcp", file)
	assert.Equal(t, "0100007F:1F90", text)

	file, text = procAddress(netip.MustParseAddrPort("[::1]:8080"))
	assert.Equal(t, "tcp6", file)
	assert.Equal(t, "00000000000000000000000001000000:1F90", text)
}

// The request goes only over a connection whose far end the configured user owns, so a
// listener swapped in after the listener check cannot answer it.
func TestPeerOwner(t *testing.T) {
	directory := t.TempDir()
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	row := func(local, remote, state string, uid int) string {
		return fmt.Sprintf("   0: %s %s %s 00000000:00000000 00:00000000 00000000 %5d        0 1 1 0\n",
			local, remote, state, uid)
	}

	require.NoError(t, os.WriteFile(filepath.Join(directory, "tcp"), []byte(header+
		row("0100007F:1F90", "00000000:0000", "0A", 1000)+
		row("0100007F:1F90", "0100007F:D431", "01", 1000)+
		row("0100007F:1F90", "0100007F:D432", "01", 2000)+
		row("0100007F:D431", "0100007F:1F90", "01", 3000)), 0o600))

	server := netip.MustParseAddrPort("127.0.0.1:8080")

	uid, err := peerOwner(directory, server, netip.MustParseAddrPort("127.0.0.1:54321"))
	require.NoError(t, err)
	assert.Equal(t, uint32(1000), uid, "serve's end of the connection, not this client's")

	uid, err = peerOwner(directory, server, netip.MustParseAddrPort("127.0.0.1:54322"))
	require.NoError(t, err)
	assert.Equal(t, uint32(2000), uid)

	_, err = peerOwner(directory, server, netip.MustParseAddrPort("127.0.0.1:54323"))
	assert.Error(t, err, "no row is a refusal, not a pass")
}

// On Linux, against a real listener, the check passes for this process's own user and
// refuses any other, through the real /proc/net.
func TestOwnerCheckOnARealConnection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the owner check needs Linux")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)

	port := server.Listener.Addr().(*net.TCPAddr).Port
	self := uint32(os.Getuid())

	client, err := New(Config{
		Host: "127.0.0.1", Port: port, EvaluateTimeout: MinEvaluateTimeout, OwnerUID: &self, ProcNet: "/proc/net",
	})
	require.NoError(t, err)

	_, err = client.Liveness(t.Context())
	require.NoError(t, err)

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, client.checkPeer(conn))

	other := self + 1
	client.config.OwnerUID = &other
	assert.Equal(t, KindWrongOwner, KindOf(client.checkPeer(conn)))
}
