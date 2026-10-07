// This root module is an anchor only: it carries no Go packages. The repo
// is split into two independently versioned modules — ./catalog and
// ./toolkit — stitched together for local development by go.work. Bazel's
// go_sdk.from_file reads the go directive below to pin the toolchain.
module github.com/dsx-ai-factory/health-validation

go 1.27.1
