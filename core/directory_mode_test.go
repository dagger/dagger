package core

import (
	"os"
	"testing"
)

func TestLayercopyModePreservesSpecialPermissionBits(t *testing.T) {
	for name, tc := range map[string]struct {
		permissions int
		expected    os.FileMode
	}{
		"setuid": {0o4755, os.FileMode(0o755) | os.ModeSetuid},
		"setgid": {0o2755, os.FileMode(0o755) | os.ModeSetgid},
		"sticky": {0o1755, os.FileMode(0o755) | os.ModeSticky},
		"all":    {0o7755, os.FileMode(0o755) | os.ModeSetuid | os.ModeSetgid | os.ModeSticky},
	} {
		t.Run(name, func(t *testing.T) {
			mode := layercopyMode(&tc.permissions)
			if mode == nil {
				t.Fatal("expected mode")
			}
			if *mode != tc.expected {
				t.Fatalf("mode = %v (%#o), expected %v (%#o)", *mode, uint32(*mode), tc.expected, uint32(tc.expected))
			}
		})
	}
}
