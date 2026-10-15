module github.com/dsx-ai-factory/health-validation/toolkit

go 1.27.2

require (
	github.com/NVIDIA/go-nvml v0.13.4-0
	github.com/dsx-ai-factory/health-validation/catalog v0.3.0
	github.com/dsx-ai-factory/health-validation/collect v0.3.0
	github.com/google/uuid v1.6.0
	go.opentelemetry.io/otel v1.46.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.46.0
	go.opentelemetry.io/otel/metric v1.46.0
	go.opentelemetry.io/otel/sdk v1.46.0
	go.opentelemetry.io/otel/sdk/metric v1.46.0
	go.opentelemetry.io/proto/otlp v1.11.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	honnef.co/go/tools v0.8.1 // indirect
)

// catalog and collect are developed in this same repo. The replaces resolve
// them from the sibling directories for in-repo builds — local dev and the
// per-module `go mod tidy` CI gate (which runs without the go.work workspace
// and otherwise could not fetch an in-repo module without git credentials).
// Consumers of a published toolkit ignore these replaces (replace directives
// apply only in the main module), so they resolve catalog and collect from the
// require versions above — keep each in lockstep with the tag you intend to
// ship against.
replace github.com/dsx-ai-factory/health-validation/catalog => ../catalog

replace github.com/dsx-ai-factory/health-validation/collect => ../collect
