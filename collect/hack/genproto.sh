#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
tools="$(mktemp -d)"
trap 'rm -rf "$tools"' EXIT

GOBIN="$tools" GOWORK=off go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
GOBIN="$tools" GOWORK=off go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

(
	cd "$root"
	PATH="$tools:$PATH" buf generate
)
