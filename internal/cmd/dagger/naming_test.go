package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLINaming(t *testing.T) {
	require.Equal(t, "e2e-test", cliName("e2eTest"))
	require.Equal(t, "http-client", cliName("HTTPClient"))
	require.Equal(t, "user-ids", cliName("userIDs"))
	require.Equal(t, "docker-config", cliName("dockerConfig"))

	// Fields are camelCase with acronyms written like words, so the new
	// rules spell myLlmMod as strcase did, but not e2eTest.
	require.Equal(t, []string{"myLlmMod"}, fieldNameCandidates("my-llm-mod"))
	require.Equal(t, []string{"e2eTest", "e2ETest"}, fieldNameCandidates("e2e-test"))
	require.Equal(t, []string{"myMod"}, fieldNameCandidates("my-mod"))
	require.Equal(t, "httpClient", gqlFieldName("HTTPClient"))
	require.Equal(t, "asJson", gqlFieldName("asJSON"))
	require.Equal(t, "HTTPClient", gqlObjectName("httpClient"))

	require.True(t, sameObjectName("MyModHTTPClient", "MyModHTTPClient"))
	require.True(t, sameObjectName("MyModHttpclient", "MyModHTTPClient"))
	require.True(t, sameObjectName("MyLlmMod", "my-llm-mod"))
}

func TestCLIFlagNormalization(t *testing.T) {
	require.Equal(t, "e2e-test", registerFlagName("e2eTest"))
	for _, typed := range []string{"e2e-test", "e2eTest", "E2ETest", "e-2-e-test", "e2e_test"} {
		require.Equal(t, "e2e-test", normalizeFlagName(typed), typed)
	}
	require.Equal(t, "docker-config", normalizeFlagName("dockerConfig"))
	require.Equal(t, "json", normalizeFlagName("json"))
	require.Equal(t, "shell-on-error", normalizeFlagName("shell-on-error"))
}

func TestFindFunctionByCmdName(t *testing.T) {
	fn2 := &modFunction{Name: "fn2"}
	fnTwo := &modFunction{Name: "fnTwo"}
	fns := []*modFunction{fn2, fnTwo}
	require.Equal(t, "fn2", fn2.CmdName())
	require.Same(t, fn2, findFunctionByCmdName(fns, "fn2"))
	// the legacy (strcase) spelling older CLIs printed
	require.Same(t, fn2, findFunctionByCmdName(fns, "fn-2"))
	require.Same(t, fnTwo, findFunctionByCmdName(fns, "fn-two"))
	require.Nil(t, findFunctionByCmdName(fns, "fn-3"))

	// a current spelling wins over another function's legacy one
	legacyClash := &modFunction{Name: "fn-2"}
	require.Same(t, legacyClash, findFunctionByCmdName([]*modFunction{fn2, legacyClash}, "fn-2"))
}

// A module's constructor is the root of its command paths by either naming
// rules' spelling, so a legacy module's collection selectors don't repeat
// the module name.
func TestChildCommandPathConstructor(t *testing.T) {
	fc := &FuncCommand{mod: &moduleDef{Name: "e2e-test"}}
	parent := &modFunction{}
	for _, constructor := range []string{"e2eTest", "e2ETest"} {
		path := fc.childCommandPath(parent, &modFunction{Name: constructor, SourceModuleName: "e2e-test"}, false)
		require.Equal(t, "e2e-test", path.Module, constructor)
		require.Empty(t, path.Fields, constructor)
	}
	path := fc.childCommandPath(parent, &modFunction{Name: "other", SourceModuleName: "e2e-test"}, false)
	require.Equal(t, []string{"other"}, path.Fields)
}
