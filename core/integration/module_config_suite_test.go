package core

import (
	"testing"

	"github.com/dagger/testctx"
)

// ModuleConfigSuite owns persisted module-config behavior. The suite is split
// across:
//   - module_config_test.go for supported module-shaped dagger.json semantics
//   - module_config_compat_test.go for old module-shaped dagger.json forms that
//     are still normalized as module config
//
// Legacy workspace inference from dagger.json belongs in
// workspace_compat_test.go instead.
type ModuleConfigSuite struct{}

func TestModuleConfig(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleConfigSuite{})
}
