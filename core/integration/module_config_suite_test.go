package core

// Workspace alignment: aligned; this is a suite-registration file for a multi-file workspace-era suite.
// Scope: Registers the module config suite and keeps shared testctx setup in one place.
// Intent: Make the module-config suite boundary explicit while current and compat coverage live in separate files.

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
