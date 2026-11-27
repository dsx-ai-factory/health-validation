# health-validation-mcp, the MCP server over health-validation serve. Run `make help` for targets.
# It is its own Go module, built with go rather than Bazel, against the checkout above it.
SHELL := /bin/bash
export GOTOOLCHAIN := local
BINARY := bin/health-validation-mcp
IMAGE ?= health-validation-mcp:local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# The catalog version the binary reports, as hv stamps its own release builds. Set it to the
# release of the serve it runs beside, such as v0.4.0, or a verdict reads as version skew.
HV_VERSION ?= dev
# Where make e2e-serve writes what your serve returned.
SERVE_RESULTS ?= $(CURDIR)/bin/test-results/serve
# The checkout above, which go.mod's replace points at. $(shell) keeps spaces in it.
PARENT := $(shell cd .. && pwd)
LDFLAGS := -s -w -X main.version=$(VERSION) -X github.com/dsx-ai-factory/health-validation/internal/version.version=$(HV_VERSION)
GOLANGCI_LINT ?= $(shell command -v golangci-lint)
# The race detector needs a 48-bit address space on arm64. Where the kernel has less (a
# kernel with 47 bits), test-race-amd64 runs it in an emulated container.
RACE_IMAGE := golang:1.27.4-bookworm@sha256:ce59b41415e2524ae99926fe1f37ce48ba8c194dcbd2a56a65436cdcefa2bd34

.PHONY: help build vet lint test test-race-amd64 docs e2e e2e-serve e2e-image evals image scan lab-test lab-fixture

help:
	@echo "  make build            build $(BINARY), static, CGO_ENABLED=0"
	@echo "  make vet              go vet"
	@echo "  make lint             gofmt, go vet, and golangci-lint when $(GOLANGCI_LINT) exists"
	@echo "  make test             unit and MCP tests with the race detector; no node, no serve"
	@echo "  make test-race-amd64  the same in an emulated amd64 container, where the race detector cannot run"
	@echo "  make docs             regenerate the catalog enums, docs/tools.md and docs/tools.json"
	@echo "  make e2e              the built binary over stdio against lab/hvfixture; writes docs/test-results/fixture"
	@echo "  make e2e-serve        the same against your own serve: HV_SERVE_PORT=<port>, optional"
	@echo "                        HV_SERVE_UID; writes $(SERVE_RESULTS), which git ignores"
	@echo "  make e2e-image        the fixture e2e with the server in its image (IMAGE=<ref>); writes nothing"
	@echo "  make evals            check every answer in docs/evals/evals.xml with the tools, against lab/hvfixture"
	@echo "  make lab-test         check that each lab/hvfixture scenario yields the verdict it claims"
	@echo "  make lab-fixture      run lab/hvfixture by hand: SCENARIO=<name> PORT=<port> [HOLD=30s]"
	@echo "  make image            build the image as $(IMAGE), with the repository root as context"
	@echo "  make scan             govulncheck, OSV, Trivy and mcp-scanner (tools must be installed)"

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/health-validation-mcp

vet:
	go vet ./...

lint: vet
	test -z "$$(gofmt -l .)"
	@if [ -n "$(GOLANGCI_LINT)" ]; then $(GOLANGCI_LINT) run ./...; else echo "golangci-lint not found: skipped"; fi

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

# GOGC=off: the Go runtime's collector crashed under qemu user emulation.
test-race-amd64:
	go mod download
	docker run --rm --platform linux/amd64 -e GOGC=off -e GOTOOLCHAIN=local -e GOFLAGS=-mod=mod \
		-e GOCACHE=/tmp/gocache -v "$(PARENT)":/src:ro -v "$$(go env GOMODCACHE)":/go/pkg/mod:ro -w /src/mcp-server \
		$(RACE_IMAGE) go test -race -count=1 -timeout 60m ./...

docs:
	go test -count=1 ./pkg/catalogview -run TestGeneratedEnumsMatchTheCatalog -update
	go test -count=1 ./pkg/server -run TestToolDocsMatchTheServer -update

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg HV_VERSION=$(HV_VERSION) -f Dockerfile -t $(IMAGE) ..

scan: build image
	govulncheck ./...
	osv-scanner scan source -r .
	trivy image --disable-telemetry --severity HIGH,CRITICAL --exit-code 1 $(IMAGE)
	mcp-scanner --analyzers yara,schema stdio --stdio-command $(CURDIR)/$(BINARY) --stdio-env HV_MCP_ALLOW_EVALUATE=true

HOLD ?= 0s
E2E := go test -tags e2e -count=1 -v -timeout 15m ./tests/e2e/

lab-test:
	go test -count=1 ./lab/hvfixture/...

lab-fixture:
	@test -n "$(SCENARIO)" -a -n "$(PORT)" || { echo "usage: make lab-fixture SCENARIO=<name> PORT=<port> [HOLD=30s]" >&2; exit 2; }
	go run ./lab/hvfixture --scenario "$(SCENARIO)" --address "127.0.0.1:$(PORT)" --hold "$(HOLD)"

e2e: lab-test
	go vet -tags e2e ./tests/...
	mkdir -p docs/test-results/fixture
	E2E_RESULTS="$(CURDIR)/docs/test-results/fixture" $(E2E)

e2e-serve:
	@test -n "$(HV_SERVE_PORT)" || { echo "set HV_SERVE_PORT to the port health-validation serve listens on" >&2; exit 2; }
	go vet -tags e2e ./tests/...
	mkdir -p "$(SERVE_RESULTS)"
	HV_SERVE_PORT=$(HV_SERVE_PORT) HV_SERVE_UID=$(HV_SERVE_UID) E2E_RESULTS="$(SERVE_RESULTS)" $(E2E)

e2e-image:
	E2E_IMAGE=$(IMAGE) $(E2E)

evals: lab-test
	go vet -tags evals ./tests/...
	go test -tags evals -count=1 -v -timeout 10m ./tests/evals/
