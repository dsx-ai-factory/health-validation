package catalog

// Override is an operator-supplied threshold value that replaces
// the catalog default for a specific check. Overrides for Fixed
// thresholds are rejected at evaluation time.
type Override struct {
	Value         any    `json:"value"`
	CheckID       string `json:"check_id"`
	ThresholdName string `json:"threshold_name"`
}

// OverrideSet holds a collection of operator overrides indexed for
// fast lookup during evaluation.
type OverrideSet struct {
	// overrides is keyed by checkID, then by threshold name.
	overrides map[string]map[string]any
}

// NewOverrideSet creates an OverrideSet from a list of overrides.
func NewOverrideSet(overrides ...Override) OverrideSet {
	os := OverrideSet{
		overrides: make(map[string]map[string]any),
	}
	for _, o := range overrides {
		if os.overrides[o.CheckID] == nil {
			os.overrides[o.CheckID] = make(map[string]any)
		}
		os.overrides[o.CheckID][o.ThresholdName] = o.Value
	}
	return os
}

// ForCheck returns the threshold overrides for a specific check.
// Returns nil if no overrides exist for the check.
func (os *OverrideSet) ForCheck(checkID string) map[string]any {
	if os.overrides == nil {
		return nil
	}
	return os.overrides[checkID]
}
