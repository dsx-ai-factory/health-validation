package health

import (
	"encoding/json"
	"testing"
)

func TestGenericDrain_JSON(t *testing.T) {
	got, err := json.Marshal(GenericDrain())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"remediation_actions":["reset_gpu","restart_os","service_required"],"operational_state":"drain"}`
	if string(got) != want {
		t.Errorf("GenericDrain() JSON = %s, want %s", got, want)
	}
}
