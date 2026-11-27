// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package hvclient talks to `health-validation serve` on the loopback interface. The
// host is fixed to 127.0.0.1 or ::1 and the operator picks the port, so no tool argument
// can point it anywhere else. It ignores proxy settings, refuses redirects, caps bodies
// and decodes verdicts strictly into health-validation's own protobuf types.
package hvclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	healthv1 "github.com/dsx-ai-factory/health-validation/proto/go/health/v1"
)

// Settings read from the environment.
const (
	EnvHost            = "HV_MCP_SERVE_HOST"
	EnvPort            = "HV_MCP_SERVE_PORT"
	EnvOwnerUID        = "HV_MCP_SERVE_UID"
	EnvEvaluateTimeout = "HV_MCP_EVALUATE_TIMEOUT_SECONDS"

	DefaultHost            = "127.0.0.1"
	DefaultPort            = 8080
	DefaultEvaluateTimeout = 150 * time.Second
	MinEvaluateTimeout     = 10 * time.Second
	MaxEvaluateTimeout     = 900 * time.Second

	MaxBodyBytes     = 4 << 20
	maxLivenessBytes = 64 << 10
	livenessTimeout  = 5 * time.Second
	dialTimeout      = 3 * time.Second
	maxHeaderBytes   = 64 << 10
)

// Config says where serve listens. Only loopback addresses are accepted.
type Config struct {
	// OwnerUID, when set, is the user that must own serve's listening socket.
	OwnerUID        *uint32
	Host            string
	ProcNet         string
	EvaluateTimeout time.Duration
	Port            int
}

// ConfigFromEnv reads the settings. A value out of range is an error, so a typo
// stops the server instead of pointing it somewhere unexpected.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	config := Config{
		Host: DefaultHost, Port: DefaultPort, EvaluateTimeout: DefaultEvaluateTimeout, ProcNet: "/proc/net",
	}

	if host := getenv(EnvHost); host != "" {
		config.Host = host
	}

	if port := getenv(EnvPort); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil {
			return config, fmt.Errorf("%s must be a number from 1024 to 65535", EnvPort)
		}

		config.Port = value
	}

	if uid := getenv(EnvOwnerUID); uid != "" {
		value, err := strconv.ParseUint(uid, 10, 32)
		if err != nil {
			return config, fmt.Errorf("%s must be a numeric user ID", EnvOwnerUID)
		}

		owner := uint32(value)
		config.OwnerUID = &owner
	}

	if seconds := getenv(EnvEvaluateTimeout); seconds != "" {
		value, err := strconv.Atoi(seconds)
		if err != nil {
			return config, fmt.Errorf("%s must be a number of seconds", EnvEvaluateTimeout)
		}

		config.EvaluateTimeout = time.Duration(value) * time.Second
	}

	return config, config.Validate()
}

// Validate refuses anything but a loopback host and a sane port and timeout.
func (c Config) Validate() error {
	if c.Host != "127.0.0.1" && c.Host != "::1" {
		return fmt.Errorf("%s must be 127.0.0.1 or ::1", EnvHost)
	}

	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("%s must be a number from 1024 to 65535", EnvPort)
	}

	if c.EvaluateTimeout < MinEvaluateTimeout || c.EvaluateTimeout > MaxEvaluateTimeout {
		return fmt.Errorf("%s must be from %d to %d", EnvEvaluateTimeout,
			int(MinEvaluateTimeout.Seconds()), int(MaxEvaluateTimeout.Seconds()))
	}

	return nil
}

// Address is host:port, with brackets for ::1.
func (c Config) Address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// Client calls serve.
type Client struct {
	http   *http.Client
	config Config
	base   string
}

// New builds a client. It never reads proxy variables and never follows a redirect.
func New(config Config) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: dialTimeout}).DialContext,
		MaxResponseHeaderBytes: maxHeaderBytes,
		MaxIdleConns:           2,
		IdleConnTimeout:        30 * time.Second,
		DisableCompression:     true,
	}

	return &Client{
		config: config,
		base:   "http://" + config.Address(),
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Config returns the settings the client uses.
func (c *Client) Config() Config { return c.config }

// StuckCall is a source call in serve that has not returned.
type StuckCall struct {
	Source string `json:"source"`
	Since  string `json:"since"`
}

// Liveness is serve's /healthz answer. It is about the process, not the node.
type Liveness struct {
	Status     string      `json:"status"`
	StuckCalls []StuckCall `json:"stuck_calls,omitempty"`
}

// Liveness reads GET /healthz. A 404 means serve runs without the liveness endpoint.
func (c *Client) Liveness(ctx context.Context) (liveness *Liveness, err error) {
	ctx, cancel := context.WithTimeout(ctx, livenessTimeout)
	defer cancel()

	status := 0
	defer logCall(ctx, http.MethodGet, "/healthz", time.Now(), &status, &err)

	if err := c.checkOwner(); err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
	if err != nil {
		return nil, &Error{Kind: KindUnexpected, cause: err}
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer func() { _ = response.Body.Close() }()

	status = response.StatusCode

	body, err := readCapped(response.Body, maxLivenessBytes)
	if err != nil {
		return nil, err
	}

	switch response.StatusCode {
	case http.StatusOK, http.StatusServiceUnavailable:
	case http.StatusNotFound:
		return &Liveness{Status: "not_available"}, nil
	default:
		return nil, statusError(response.StatusCode)
	}

	liveness = &Liveness{}

	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(liveness); err != nil {
		return nil, &Error{Kind: KindBadResponse, cause: err}
	}

	if liveness.Status != "ok" && liveness.Status != "unhealthy" {
		return nil, &Error{Kind: KindBadResponse, cause: fmt.Errorf("unknown liveness status %q", liveness.Status)}
	}

	return liveness, nil
}

// EvaluateRequest holds the only fields a caller may set. Everything else in serve's
// request keeps serve's default.
type EvaluateRequest struct {
	Cadence *healthv1.SchedulingCadence
	XidFrom *int64
}

// Evaluate posts one evaluation request and returns the decoded verdict.
func (c *Client) Evaluate(ctx context.Context, in EvaluateRequest) (verdict *healthv1.NodeVerdict, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.config.EvaluateTimeout)
	defer cancel()

	status := 0
	defer logCall(ctx, http.MethodPost, "/verdict", time.Now(), &status, &err)

	if err := c.checkOwner(); err != nil {
		return nil, err
	}

	body, err := protojson.Marshal(&healthv1.EvaluateRequest{Cadence: in.Cadence, XidFrom: in.XidFrom})
	if err != nil {
		return nil, &Error{Kind: KindUnexpected, cause: err}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/verdict", bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Kind: KindUnexpected, cause: err}
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer func() { _ = response.Body.Close() }()

	status = response.StatusCode

	if response.StatusCode != http.StatusOK {
		// Drain a little so the connection can be reused; the text is never shown.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxLivenessBytes))

		return nil, statusError(response.StatusCode)
	}

	raw, err := readCapped(response.Body, MaxBodyBytes)
	if err != nil {
		if ctx.Err() != nil {
			return nil, transportError(ctx, err)
		}

		return nil, err
	}

	return DecodeVerdict(raw)
}

// logCall writes one debug line per call to serve: never a body, never serve's text.
func logCall(ctx context.Context, method, path string, start time.Time, status *int, err *error) {
	attributes := []any{
		"method", method, "path", path, "status", *status,
		"seconds", time.Since(start).Round(time.Millisecond).Seconds(),
	}
	if *err != nil {
		attributes = append(attributes, "error_kind", string(KindOf(*err)))
	}

	slog.DebugContext(ctx, "call to serve", attributes...)
}

func readCapped(body io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, &Error{Kind: KindUnexpected, cause: err}
	}

	if int64(len(raw)) > limit {
		return nil, &Error{Kind: KindTooLarge, cause: fmt.Errorf("body over %d bytes", limit)}
	}

	return raw, nil
}

func transportError(ctx context.Context, err error) error {
	var netError net.Error

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &Error{Kind: KindTimeout, cause: err}
	case ctx.Err() != nil:
		return &Error{Kind: KindCancelled, cause: err}
	case isConnectionRefused(err):
		return &Error{Kind: KindNotRunning, cause: err}
	case errors.As(err, &netError) && netError.Timeout():
		return &Error{Kind: KindTimeout, cause: err}
	}

	return &Error{Kind: KindUnreachable, cause: err}
}
