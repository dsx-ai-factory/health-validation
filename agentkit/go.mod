module github.com/dsx-ai-factory/health-validation/agentkit

go 1.27.0

// collect is developed in this same repo. The replace resolves it from the
// local tree; consumers of a published agentkit ignore this replace (replace
// directives apply only in the main module).
replace github.com/dsx-ai-factory/health-validation/collect => ../collect

replace github.com/dsx-ai-factory/health-validation/catalog => ../catalog

require (
	github.com/dsx-ai-factory/health-validation/catalog v0.0.0-00010101000000-000000000000
	github.com/dsx-ai-factory/health-validation/collect v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/soheilhy/cmux v0.1.5
	github.com/stretchr/testify v1.11.1
	google.golang.org/grpc v1.83.1
	google.golang.org/protobuf v1.36.11
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/NVIDIA/go-dcgm v0.0.0-20260214140307-ad5d86fe3d32 // indirect
	github.com/NVIDIA/go-nvlib v0.12.0 // indirect
	github.com/NVIDIA/go-nvml v0.13.3-1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/bits-and-blooms/bitset v1.22.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/tools v0.46.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.8.1 // indirect
)
