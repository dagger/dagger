package llmconfig

import (
	"os/exec"
	"testing"
)

func TestOpListVaultsNoOp(t *testing.T) {
	// If op isn't available or not signed in, should return nil gracefully
	if _, err := exec.LookPath("op"); err != nil {
		vaults := opListVaults()
		if vaults != nil {
			t.Errorf("expected nil when op is not installed, got %v", vaults)
		}
	}
}

func TestOpListItemsNoOp(t *testing.T) {
	if _, err := exec.LookPath("op"); err != nil {
		items := opListItems("nonexistent")
		if items != nil {
			t.Errorf("expected nil when op is not installed, got %v", items)
		}
	}
}

func TestOpListFieldsNoOp(t *testing.T) {
	if _, err := exec.LookPath("op"); err != nil {
		fields := opListFields("nonexistent", "nonexistent")
		if fields != nil {
			t.Errorf("expected nil when op is not installed, got %v", fields)
		}
	}
}
