// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package hvclient

import (
	"errors"
	"fmt"
	"net/http"
	"syscall"
)

// Kind names a failure without its detail. The detail (addresses, serve's own error
// text) stays in the server's log.
type Kind string

// The failure kinds a caller can see.
const (
	KindNotRunning        Kind = "service_not_running"
	KindUnreachable       Kind = "service_unreachable"
	KindWrongOwner        Kind = "wrong_owner"
	KindBusy              Kind = "busy"
	KindTimeout           Kind = "timeout"
	KindCancelled         Kind = "cancelled"
	KindBadRequest        Kind = "bad_request"
	KindUnavailable       Kind = "unavailable"
	KindServerError       Kind = "server_error"
	KindRedirect          Kind = "redirect_refused"
	KindTooLarge          Kind = "too_large"
	KindBadResponse       Kind = "bad_response"
	KindBadVerdict        Kind = "bad_verdict"
	KindUnsupportedSchema Kind = "unsupported_schema"
	KindUnexpected        Kind = "unexpected"
)

var hints = map[Kind]string{
	KindNotRunning: "nothing answers on the configured port; start health-validation serve, or check " +
		"HV_MCP_SERVE_PORT",
	KindUnreachable: "the configured address did not answer; check serve with hv_service_status, then try " +
		"again after the evaluate interval",
	KindWrongOwner: "the port is not held by the user HV_MCP_SERVE_UID names, so the server refused to talk " +
		"to it; check which process holds the port",
	KindBusy: "serve is at its request limit; wait and try again, it is not retried automatically",
	KindTimeout: "serve did not answer in time; call hv_service_status to see stuck source calls, and set " +
		"HV_MCP_EVALUATE_TIMEOUT_SECONDS above serve's --request-timeout",
	KindCancelled:  "the call was cancelled; call the tool again if a verdict is still needed",
	KindBadRequest: "serve refused the request; this is a bug in this server, so report it",
	KindUnavailable: "serve said it is unavailable; check it with hv_service_status, then try again after " +
		"the evaluate interval",
	KindServerError: "serve could not produce a verdict; check serve's log, and do not schedule on the node " +
		"until a verdict comes back",
	KindRedirect: "serve answered with a redirect, which is refused; check that the configured port is " +
		"health-validation serve",
	KindTooLarge: "the answer was larger than the 4 MiB cap; check that the configured port is " +
		"health-validation serve",
	KindBadResponse: "the answer was not in the expected shape; check that the configured port is " +
		"health-validation serve",
	KindBadVerdict: "the verdict could not be decoded or failed validation, so it is not trusted; check " +
		"serve's log, and do not schedule on the node until a verdict comes back",
	KindUnsupportedSchema: "the verdict uses a schema version this server does not support; run serve built " +
		"from the same commit as this server, which reads schema 0.7",
	KindUnexpected: "an unexpected error happened; check this server's log on stderr",
}

// Error is a failed call to serve.
type Error struct {
	cause  error
	Kind   Kind
	Status int
}

// Error is safe to show to a caller: it holds no address or serve text.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, hints[e.Kind])
}

// Unwrap gives the detail, for the server's log only.
func (e *Error) Unwrap() error { return e.cause }

// Detail is the full text for the log.
func (e *Error) Detail() string {
	if e.cause == nil {
		return e.Error()
	}

	return e.Error() + ": " + e.cause.Error()
}

// KindOf returns the kind of a client error, or KindUnexpected.
func KindOf(err error) Kind {
	var clientError *Error
	if errors.As(err, &clientError) {
		return clientError.Kind
	}

	return KindUnexpected
}

func statusError(code int) *Error {
	kind := KindUnexpected

	switch {
	case code == http.StatusBadRequest:
		kind = KindBadRequest
	case code == http.StatusTooManyRequests:
		kind = KindBusy
	case code == http.StatusServiceUnavailable:
		kind = KindUnavailable
	case code == http.StatusGatewayTimeout:
		kind = KindTimeout
	case code == http.StatusRequestTimeout:
		kind = KindCancelled
	case code >= 300 && code < 400:
		kind = KindRedirect
	case code >= 500:
		kind = KindServerError
	}

	return &Error{Kind: kind, Status: code, cause: fmt.Errorf("HTTP status %d", code)}
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
