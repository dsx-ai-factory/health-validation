# The MCP server as one static binary on distroless: no shell, no package manager, no
# libc, a non-root user. Bases are pinned by digest, Go dependencies by go.sum. It needs
# no GPU, devices or journal: it talks to health-validation serve on loopback. See
# docs/security.md to run it. The module builds against the checkout, so the build
# context is the repository root: `make image`, or `docker build -f mcp-server/Dockerfile .`.
FROM golang:1.27.4-trixie@sha256:430985542100b871e108ccc00e8dcc5eef08ca14ee5825e98ebf563d8ffec4ee AS build
ARG VERSION=dev
ARG HV_VERSION=dev
WORKDIR /src
COPY . .
WORKDIR /src/mcp-server
RUN CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X github.com/dsx-ai-factory/health-validation/internal/version.version=${HV_VERSION}" \
      -o /health-validation-mcp ./cmd/health-validation-mcp

FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --chmod=555 --from=build /health-validation-mcp /health-validation-mcp
USER 65532:65532
ENTRYPOINT ["/health-validation-mcp"]
