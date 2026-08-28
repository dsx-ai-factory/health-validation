module github.com/dsx-ai-factory/health-validation/toolkit

go 1.27.0

require (
	github.com/NVIDIA/go-nvml v0.13.0-1
	github.com/dsx-ai-factory/health-validation/catalog v0.1.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cel.dev/expr v0.25.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240826202546-f6391c0de4c7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240826202546-f6391c0de4c7 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	honnef.co/go/tools v0.6.1 // indirect
)

// catalog is developed in this same repo. The replace resolves it from the
// sibling directory for in-repo builds — local dev and the per-module
// `go mod tidy` CI gate (which runs without the go.work workspace and otherwise
// could not fetch an in-repo module without git credentials). Consumers of a
// published toolkit ignore this replace (replace directives apply only in the
// main module), so they resolve catalog from the require version above — keep
// that version in lockstep with the catalog tag you intend to ship against.
replace github.com/dsx-ai-factory/health-validation/catalog => ../catalog
