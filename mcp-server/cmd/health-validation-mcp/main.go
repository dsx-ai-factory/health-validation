// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package main runs the MCP server for NVIDIA health-validation, over stdio.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/catalogview"
	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/hvclient"
	"github.com/dsx-ai-factory/health-validation/mcp-server/pkg/server"
)

// Populated during the build.
var version = "dev"

// A cancelled evaluation ends at once; this only bounds a stuck HTTP close.
const shutdownTimeout = 10 * time.Second

const usage = `health-validation-mcp: an MCP server for NVIDIA health-validation.

It speaks MCP over stdin and stdout, so an MCP client starts it. Run by hand, it logs one
line to stderr and then waits for a client: press Ctrl-C to stop it.

Usage:
  health-validation-mcp [flags]

Flags:
  -h, --help     show this help
  -v, --version  show the version
  -d, --debug    log each call to serve to stderr

Environment:
  HV_MCP_SERVE_HOST                 127.0.0.1 (default) or ::1, where serve listens
  HV_MCP_SERVE_PORT                 serve's port, 1024 to 65535 (default 8080)
  HV_MCP_SERVE_UID                  if set, the user that must own serve's socket
  HV_MCP_ALLOW_EVALUATE             exactly "true" adds hv_get_verdict, which runs a real
                                    evaluation on the node
  HV_MCP_EVALUATE_INTERVAL_SECONDS  at most one evaluation per this many seconds, 5 to 600
                                    (default 10)
  HV_MCP_EVALUATE_TIMEOUT_SECONDS   how long to wait for a verdict, 10 to 900 (default 150)

Docs: https://github.com/dsx-ai-factory/health-validation/tree/main/mcp-server
`

func main() {
	options, exitCode, done := parseFlags(os.Args[1:], os.Stdout, os.Stderr)
	if done {
		os.Exit(exitCode)
	}

	if err := run(options, os.Getenv, os.Stdin, os.Stdout, os.Stderr); err != nil {
		slog.Error("the server stopped", "error", err)
		os.Exit(1)
	}
}

type options struct {
	debug bool
}

// parseFlags reads the command line. When done is true, the program ends with exitCode.
func parseFlags(args []string, stdout, stderr io.Writer) (parsed options, exitCode int, done bool) {
	flags := flag.NewFlagSet("health-validation-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var showHelp, showVersion bool

	flags.BoolVar(&showHelp, "h", false, "")
	flags.BoolVar(&showHelp, "help", false, "")
	flags.BoolVar(&showVersion, "v", false, "")
	flags.BoolVar(&showVersion, "version", false, "")
	flags.BoolVar(&parsed.debug, "d", false, "")
	flags.BoolVar(&parsed.debug, "debug", false, "")

	if err := flags.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n\n%s", err, usage)
		return parsed, 2, true
	}

	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected argument %q\n\n%s", flags.Arg(0), usage)
		return parsed, 2, true
	}

	switch {
	case showHelp:
		_, _ = fmt.Fprint(stdout, usage)
		return parsed, 0, true
	case showVersion:
		_, _ = fmt.Fprintf(stdout, "%s (health-validation catalog %s)\n", buildVersion(), catalogview.Version())
		return parsed, 0, true
	}

	return parsed, 0, false
}

// buildVersion falls back to the module version, which `go install ...@vX.Y.Z` records.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" &&
		info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}

func run(opts options, getenv func(string) string, stdin io.ReadCloser, stdout io.WriteCloser, stderr io.Writer) error {
	level := slog.LevelInfo
	if opts.debug {
		level = slog.LevelDebug
	}

	// Stderr only: stdout belongs to the MCP protocol.
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	clientConfig, err := hvclient.ConfigFromEnv(getenv)
	if err != nil {
		return err
	}

	serverConfig, err := server.ConfigFromEnv(getenv, logger)
	if err != nil {
		return err
	}

	client, err := hvclient.New(clientConfig)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// After the first signal the default action is back, so a second one ends the process.
	context.AfterFunc(ctx, stop)

	// Handlers follow this, not only their request: on stdio the SDK waits for running
	// handlers when ctx ends but does not cancel them. Closing stdin does cancel them.
	shutdown, cancelShutdown := context.WithCancel(ctx)
	defer cancelShutdown()

	mcpServer := server.New(shutdown, buildVersion(), serverConfig, client, logger)

	logger.Info("started: waiting for an MCP client on stdin (see --help)",
		"version", buildVersion(), "catalog", catalogview.Version(), "serve", clientConfig.Address(),
		"evaluateAllowed", serverConfig.AllowEvaluate, "ownerCheck", clientConfig.OwnerUID != nil)

	runErr := mcpServer.MCP().Run(ctx, &server.StdioTransport{Reader: stdin, Writer: stdout, Logger: logger})

	cancelShutdown()

	waitCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if waitErr := mcpServer.Wait(waitCtx); waitErr != nil {
		return waitErr
	}

	if runErr == nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, mcp.ErrConnectionClosed) ||
		errors.Is(runErr, io.EOF) {
		return nil
	}

	return runErr
}
