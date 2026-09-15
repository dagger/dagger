package modules_test

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
)

func TestModuleCodegenConfig_Clone(t *testing.T) {
	tru := true

	orig := &modules.ModuleCodegenConfig{
		AutomaticGitignore: &tru,
	}
	clone := orig.Clone()

	// flipping clone fields must not affect the original
	*clone.AutomaticGitignore = false

	if *orig.AutomaticGitignore != true {
		t.Errorf("Clone aliased AutomaticGitignore: got %v, want true", *orig.AutomaticGitignore)
	}
}
