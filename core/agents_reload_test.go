package core

import (
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestToolStateIdentity(t *testing.T) {
	dag := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(dag)
	makeObject := func(src *ModuleSource, name, typeName string) *ModuleObject {
		var source dagql.Nullable[dagql.ObjectResult[*ModuleSource]]
		if src != nil {
			source = dagql.NonNull(newTypeDefDetachedResult(t, dag, "reload-source", src))
		}
		mod := newTypeDefDetachedResult(t, dag, "reload-module", &Module{
			NameField: name, OriginalName: "staff", Source: source,
		})
		return &ModuleObject{Module: mod, TypeDef: &ObjectTypeDef{OriginalName: typeName}}
	}
	gitSource := func(repo, revision, subpath string) *ModuleSource {
		return &ModuleSource{
			Kind: ModuleSourceKindGit, SourceRootSubpath: subpath,
			Git: &GitModuleSource{CloneRef: repo, Commit: revision, Version: revision},
		}
	}
	original := makeObject(gitSource("https://github.com/vito/agents", "old", "staff"), "staff", "Staff")
	for _, tc := range []struct {
		name string
		src  *ModuleSource
	}{
		{"new revision", gitSource("https://github.com/vito/agents", "new", "staff")},
		{"fork", gitSource("https://github.com/other/agents", "new", "staff")},
		{"different subdirectory", gitSource("https://github.com/vito/agents", "new", "other")},
		{"local clone", &ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/clone"}}},
		{"cached implementation without stable source metadata", &ModuleSource{Kind: ModuleSourceKindDir}},
		{"absent source metadata", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := makeObject(tc.src, "staff", "Staff")
			require.NoError(t, sameToolStateIdentity(original, next))
		})
	}
	for _, tc := range []struct {
		name, alias, module, object string
	}{
		{"different installation", "other", "staff", "Staff"},
		{"different intrinsic module", "staff", "other", "Staff"},
		{"different object", "staff", "staff", "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := makeObject(nil, tc.alias, tc.object)
			next.Module.Self().OriginalName = tc.module
			require.ErrorContains(t, sameToolStateIdentity(original, next), "module or object identity changed")
		})
	}
	t.Run("local roots can move", func(t *testing.T) {
		old := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/one"}}, "staff", "Staff")
		next := makeObject(&ModuleSource{Kind: ModuleSourceKindLocal, SourceRootSubpath: "staff", Local: &LocalModuleSource{ContextDirectoryPath: "/two"}}, "staff", "Staff")
		require.NoError(t, sameToolStateIdentity(old, next))
	})
}

// Only two different modules' bindings conflict. An unowned binding, e.g. one
// a tool continuation or the main client made, may be taken over by a module's
// binding of the same type on reload, and vice versa.
func TestRecomposeToolOwners(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(srv)
	dagql.Fields[*LLM]{}.Install(srv)
	tool := newTypeDefDetachedResult(t, srv, "tools", &Module{NameField: "tools"})
	makeLLM := func(owner string) dagql.ObjectResult[*LLM] {
		return newTypeDefDetachedResult(t, srv, "llm", &LLM{mcp: &MCP{mu: new(sync.Mutex), boundTools: []boundTool{{object: tool, Owner: owner}}}})
	}
	_, err := preserveRecomposedTools(t.Context(), srv, makeLLM("old-module"), makeLLM("new-module"))
	require.ErrorContains(t, err, `tool binding "Module" owned by other expertise: module "old-module" -> module "new-module"`)
	require.ErrorContains(t, err, "use a fresh composition")
	for _, owners := range [][2]string{{"", "module"}, {"module", ""}, {"module", "module"}, {"", ""}} {
		_, err := preserveRecomposedTools(t.Context(), srv, makeLLM(owners[0]), makeLLM(owners[1]))
		require.NoError(t, err, "%q -> %q", owners[0], owners[1])
	}
}

func TestExpertiseIdentity(t *testing.T) {
	entry := &Expertise{Artifact: &Artifact{ModuleName: "staff", Path: []string{"staff", "worker"}}}
	first, err := entry.Identity()
	require.NoError(t, err)
	require.Equal(t, "dag://staff/worker", first)
	cloned, err := entry.Clone().Identity()
	require.NoError(t, err)
	require.Equal(t, first, cloned)
	entry.Artifact.Path = []string{"staff", "agent"}
	other, err := entry.Identity()
	require.NoError(t, err)
	require.NotEqual(t, first, other)
}

// Contributions are stamped with the current module's installed name, and an
// entry refreshes the contributions stamped with its module's: both must name
// an aliased install by its alias, not by the module's intrinsic name.
func TestCompositionOwnerIsInstalledName(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	installModuleObjectTestModuleClass(srv)
	aliased := &Module{NameField: "alias", OriginalName: "intrinsic"}
	require.Equal(t, "alias", CompositionOwner(aliased))
	require.Empty(t, CompositionOwner(nil), "the main client is unowned")
	mod := newTypeDefDetachedResult(t, srv, "aliased-module", aliased)
	entry := &Expertise{Artifact: &Artifact{Path: []string{"alias", "agent"}, Node: &ModTreeNode{Name: "agent", Module: mod, OriginalModule: mod}}}
	require.Equal(t, CompositionOwner(aliased), entry.ModuleName())
}

func TestExpertiseConversion(t *testing.T) {
	srv := newCoreDagqlServerForTest(t, &Query{})
	installTypeDefTestClasses(srv)
	installModuleObjectTestModuleClass(srv)
	mod := newTypeDefDetachedResult(t, srv, "agent-module", &Module{NameField: "agent"})
	objectType := func(name string) dagql.ObjectResult[*TypeDef] {
		obj := newTypeDefDetachedResult(t, srv, name, &ObjectTypeDef{Name: name})
		return newTypeDefDetachedResult(t, srv, name+"-type", (&TypeDef{}).WithObject(obj))
	}
	llmType, agentType, wsType := objectType("LLM"), objectType("Agent"), objectType("Workspace")
	stringType := newTypeDefDetachedResult(t, srv, "string", &TypeDef{Kind: TypeDefKindString})
	arg := func(name string, typ dagql.ObjectResult[*TypeDef]) dagql.ObjectResult[*FunctionArg] {
		return newTypeDefDetachedResult(t, srv, name, &FunctionArg{Name: name, TypeDef: typ})
	}
	for _, tc := range []struct {
		name  string
		ret   dagql.ObjectResult[*TypeDef]
		args  dagql.ObjectResultArray[*FunctionArg]
		valid bool
	}{
		{"plain transform", llmType, dagql.ObjectResultArray[*FunctionArg]{arg("base", llmType)}, true},
		{"owned transform", llmType, dagql.ObjectResultArray[*FunctionArg]{arg("base", llmType), arg("me", agentType), arg("ws", wsType)}, true},
		{"no conversation", llmType, nil, false},
		{"two conversations", llmType, dagql.ObjectResultArray[*FunctionArg]{arg("base", llmType), arg("other", llmType)}, false},
		{"caller argument", llmType, dagql.ObjectResultArray[*FunctionArg]{arg("base", llmType), arg("name", stringType)}, false},
		{"wrong return", stringType, dagql.ObjectResultArray[*FunctionArg]{arg("base", llmType)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &Function{Name: "configure", ReturnType: tc.ret, Args: tc.args}
			fnResult := newTypeDefDetachedResult(t, srv, "configure", fn)
			obj := newTypeDefDetachedResult(t, srv, "parent", &ObjectTypeDef{Name: "Fixture", Functions: dagql.ObjectResultArray[*Function]{fnResult}})
			parentType := newTypeDefDetachedResult(t, srv, "parent-type", (&TypeDef{}).WithObject(obj))
			artifact := &Artifact{Path: []string{"agent", "configure"}, TypeName: "LLM", Node: &ModTreeNode{Name: "configure", OriginalModule: mod, Parent: &ModTreeNode{Type: parentType}}}
			entry, err := NewExpertise(artifact)
			if !tc.valid {
				require.ErrorContains(t, err, "not a source of expertise")
				require.Empty(t, ConversationArgument(artifact))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "base", ConversationArgument(artifact))
			require.False(t, functionRequiresCallerArgs(fn), "transforms must remain discoverable without @agent")
			require.Equal(t, "agent", entry.OriginalModule().Name())
			entry.Artifact.Node.Name = "changed"
			require.Equal(t, "configure", artifact.Node.Name)
		})
	}
}
