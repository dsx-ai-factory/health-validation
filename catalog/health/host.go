package health

// Host is what a run knows about the node it is evaluating, independent
// of any one measurement. It is supplied once, when the catalog is built,
// and holds for every check and measurement in that run.
type Host struct {
	// Architecture is the node's GPU architecture (e.g. "GB200"). Checks
	// scoped to other architectures are skipped, and per-architecture
	// thresholds resolve against it.
	Architecture string
}
