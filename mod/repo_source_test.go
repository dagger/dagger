package mod

import (
	"context"
	"testing"
)

func TestVersionConstraint(t *testing.T) {
	ctx := context.TODO()

	versions := []string{
		"v0.0.5",
		"v0.1.0",
		"v1.0.0",
		"v1.1.0",
		"v1.2.0",
		"v2.0.0",
	}

	tagVersion, err := upgradeToLatestVersion(ctx, "test", versions, "0.0.1", "<= 1.1.0")
	if err != nil {
		t.Error(err)
	}

	// Make sure we select the right version based on constraint
	if tagVersion != "v1.1.0" {
		t.Errorf("wrong version: expected v1.1.0, got %v", tagVersion)
	}

	// Make sure an invalid constraint (version out of range) returns an error
	_, err = upgradeToLatestVersion(ctx, "test", versions, "0.0.1", "> 99999")
	if err == nil {
		t.Error("selected wrong version based on constraint")
	}

	// Make sure a version can't downgrade
	_, err = upgradeToLatestVersion(ctx, "test", versions, "5.0.0", "<= 1.1.0")
	if err == nil {
		t.Error("selected an unavailable version")
	}
}
