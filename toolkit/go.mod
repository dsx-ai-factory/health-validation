module github.com/dsx-ai-factory/health-validation/toolkit

go 1.27.0

require (
	github.com/NVIDIA/go-nvml v0.13.4-0
	github.com/dsx-ai-factory/health-validation/catalog v0.2.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	honnef.co/go/tools v0.8.1 // indirect
)

// catalog is developed in this same repo. The replace resolves it from the
// sibling directory for in-repo builds — local dev and the per-module
// `go mod tidy` CI gate (which runs without the go.work workspace and otherwise
// could not fetch an in-repo module without git credentials). Consumers of a
// published toolkit ignore this replace (replace directives apply only in the
// main module), so they resolve catalog from the require version above — keep
// that version in lockstep with the catalog tag you intend to ship against.
replace github.com/dsx-ai-factory/health-validation/catalog => ../catalog
