module github.com/dsx-ai-factory/health-validation/agentkit

go 1.27.2

// collect is developed in this same repo. The replace resolves it from the
// local tree; consumers of a published agentkit ignore this replace (replace
// directives apply only in the main module).
replace github.com/dsx-ai-factory/health-validation/collect => ../collect

replace github.com/dsx-ai-factory/health-validation/catalog => ../catalog

replace github.com/dsx-ai-factory/health-validation/toolkit => ../toolkit

require (
	github.com/dsx-ai-factory/health-validation/catalog v0.3.0
	github.com/dsx-ai-factory/health-validation/collect v0.3.0
	github.com/dsx-ai-factory/health-validation/toolkit v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/soheilhy/cmux v0.1.5
	github.com/stretchr/testify v1.12.1
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/NVIDIA/go-dcgm v1.4701.2 // indirect
	github.com/NVIDIA/go-nvlib v0.13.0 // indirect
	github.com/NVIDIA/go-nvml v0.13.4-1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/bits-and-blooms/bitset v1.25.0 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	honnef.co/go/tools v0.8.2 // indirect
)
