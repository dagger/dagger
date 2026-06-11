package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/querybuilder"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

type ModTreePathTestSuite struct {
	suite.Suite
}

type ModTreeNodeTestSuite struct {
	suite.Suite
}

func TestModTreePath(t *testing.T) {
	testctx.New(t).RunTests(ModTreePathTestSuite{})
}

func TestModTreeNode(t *testing.T) {
	testctx.New(t).RunTests(ModTreeNodeTestSuite{})
}

func (s *ModTreeNodeTestSuite) TestBuildScaleOutModuleQueryForModuleLoadedFromDirectory(ctx context.Context, t *testctx.T) {
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	dag := newCoreDagqlServerForTest(t, &Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Directory]{Typed: &Directory{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))

	sourceDir := newTypeDefAttachedResult(t, ctx, cache, dag, "sourceDir", &Directory{})
	source := newTypeDefAttachedResult(t, ctx, cache, dag, "moduleSourceFromDirectory", &ModuleSource{
		Kind: ModuleSourceKindDir,
		DirSrc: &DirModuleSource{
			OriginalContextDir:        sourceDir,
			OriginalSourceRootSubpath: "src",
		},
	})
	mod := newTypeDefAttachedResult(t, ctx, cache, dag, "module", &Module{
		Source: dagql.NonNull(source),
	})

	query, err := (&ModTreeNode{Module: mod}).buildScaleOutModuleQuery(querybuilder.Query())
	require.NoError(t, err)

	generated, err := query.Select("id").Build(ctx)
	require.NoError(t, err)

	schema, err := gqlparser.LoadSchema(&ast.Source{Input: `
		schema {
			query: Query
		}

		interface Node {
			id: ID!
		}

		type Query {
			node(id: ID!): Node
		}

		type Directory implements Node {
			id: ID!
			asModuleSource(sourceRootPath: String!): ModuleSource!
		}

		type ModuleSource {
			_asModule(
				legacyDefaultPath: Boolean = false,
				legacyNameOverride: String = "",
				legacyWorkspaceConfigJson: String = "",
				legacyDefaultsFromDotEnv: Boolean = false,
				legacyArgCustomizationsJson: String = "",
				defaultPathContextSourceRef: String = "",
				defaultPathContextSourcePin: String = "",
			): Module!
		}

		type Module {
			id: ID!
		}
	`})
	require.NoError(t, err)

	doc, err := parser.ParseQuery(&ast.Source{Input: generated})
	require.NoError(t, err)
	require.Empty(t,
		validator.ValidateWithRules(schema, doc, rules.NewDefaultRules()),
		"generated query should validate:\n%s", generated,
	)
}

func (s *ModTreeNodeTestSuite) TestBuildScaleOutModuleQueryPreservesAsModuleOptions(ctx context.Context, t *testctx.T) {
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	dag := newCoreDagqlServerForTest(t, &Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleSource]{Typed: &ModuleSource{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))

	source := newTypeDefAttachedResult(t, ctx, cache, dag, "moduleSource", &ModuleSource{
		Kind:              ModuleSourceKindLocal,
		ModuleName:        "go",
		SourceRootSubpath: "toolchains/go",
		Local: &LocalModuleSource{
			ContextDirectoryPath: "/repo",
		},
	})
	contextSource := newTypeDefAttachedResult(t, ctx, cache, dag, "contextSource", &ModuleSource{
		Kind:              ModuleSourceKindLocal,
		SourceRootSubpath: ".",
		Local: &LocalModuleSource{
			ContextDirectoryPath: "/repo",
		},
	})
	mod := newTypeDefAttachedResult(t, ctx, cache, dag, "module", &Module{
		Source:            dagql.NonNull(source),
		ContextSource:     dagql.NonNull(contextSource),
		NameField:         "golang",
		LegacyDefaultPath: true,
		LegacyArgCustomizations: []*modules.ModuleConfigArgument{
			{Argument: "selectModule", Default: "e2e/*"},
		},
		WorkspaceConfig:    map[string]any{"target": "default"},
		DefaultsFromDotEnv: true,
	})

	query, err := (&ModTreeNode{Module: mod}).buildScaleOutModuleQuery(querybuilder.Query())
	require.NoError(t, err)

	generated, err := query.Select("id").Build(ctx)
	require.NoError(t, err)
	require.Contains(t, generated, `legacyNameOverride: "golang"`)
	require.Contains(t, generated, `legacyDefaultPath: true`)
	require.Contains(t, generated, `defaultPathContextSourceRef: "/repo"`)
	require.Contains(t, generated, `legacyWorkspaceConfigJson:`)
	require.Contains(t, generated, `legacyDefaultsFromDotEnv: true`)
	require.Contains(t, generated, `legacyArgCustomizationsJson:`)
	require.Contains(t, generated, `selectModule`)

	// The query executes on the remote engine as an external request, where
	// internal arguments are rejected: the legacy options must go through
	// _asModule, which accepts them as ordinary arguments.
	require.Contains(t, generated, `_asModule(`)
	require.NotContains(t, generated, ` asModule(`)
}

func (s *ModTreeNodeTestSuite) TestModuleLocalPathString(ctx context.Context, t *testctx.T) {
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)

	dag := newCoreDagqlServerForTest(t, &Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))

	mod := newTypeDefAttachedResult(t, ctx, cache, dag, "module", &Module{})

	moduleRoot := &ModTreeNode{Module: mod}
	require.Equal(t, "generate-dagger-runtimes", (&ModTreeNode{
		Parent: moduleRoot,
		Name:   "GenerateDaggerRuntimes",
		Module: mod,
	}).moduleLocalPathString())

	workspaceRoot := &ModTreeNode{
		Parent: &ModTreeNode{},
		Name:   "Go",
		Module: mod,
	}
	require.Equal(t, "generate-dagger-runtimes", (&ModTreeNode{
		Parent: workspaceRoot,
		Name:   "GenerateDaggerRuntimes",
		Module: mod,
	}).moduleLocalPathString())

	workspaceNested := &ModTreeNode{
		Parent: workspaceRoot,
		Name:   "TestSplit",
		Module: mod,
	}
	require.Equal(t, "test-split:test-cli-engine", (&ModTreeNode{
		Parent: workspaceNested,
		Name:   "TestCliEngine",
		Module: mod,
	}).moduleLocalPathString())
}

func (s *ModTreePathTestSuite) TestIsParentOf(ctx context.Context, t *testctx.T) {
	testCases := []struct {
		name     string
		parent   ModTreePath
		child    ModTreePath
		expected bool
	}{
		{
			name:     "direct parent",
			parent:   ModTreePath{"foo"},
			child:    ModTreePath{"foo", "bar"},
			expected: true,
		},
		{
			name:     "grandparent",
			parent:   ModTreePath{"foo"},
			child:    ModTreePath{"foo", "bar", "baz"},
			expected: true,
		},
		{
			name:     "same path",
			parent:   ModTreePath{"foo", "bar"},
			child:    ModTreePath{"foo", "bar"},
			expected: true,
		},
		{
			name:     "child is shorter - not parent",
			parent:   ModTreePath{"foo", "bar"},
			child:    ModTreePath{"foo"},
			expected: false,
		},
		{
			name:     "different root - not parent",
			parent:   ModTreePath{"foo"},
			child:    ModTreePath{"bar", "baz"},
			expected: false,
		},
		{
			name:     "different middle segment - not parent",
			parent:   ModTreePath{"foo", "bar"},
			child:    ModTreePath{"foo", "baz", "qux"},
			expected: false,
		},
		{
			name:     "empty parent of non-empty",
			parent:   ModTreePath{},
			child:    ModTreePath{"foo"},
			expected: true,
		},
		{
			name:     "empty contains empty",
			parent:   ModTreePath{},
			child:    ModTreePath{},
			expected: true,
		},
		{
			name:     "api case parent with cli case child",
			parent:   ModTreePath{"fooBar"},
			child:    ModTreePath{"foo-bar", "baz"},
			expected: true,
		},
		{
			name:     "cli case parent with api case child",
			parent:   ModTreePath{"foo-bar"},
			child:    ModTreePath{"fooBar", "baz"},
			expected: true,
		},
		{
			name:     "mixed case - api parent segments with cli child segments",
			parent:   ModTreePath{"myService", "getUserInfo"},
			child:    ModTreePath{"my-service", "get-user-info", "by-id"},
			expected: true,
		},
		{
			name:     "mixed case - cli parent segments with api child segments",
			parent:   ModTreePath{"my-service", "get-user-info"},
			child:    ModTreePath{"myService", "getUserInfo", "byId"},
			expected: true,
		},
		{
			name:     "complex mixed case matching",
			parent:   ModTreePath{"fooBar", "bazQux"},
			child:    ModTreePath{"foo-bar", "baz-qux", "test-case"},
			expected: true,
		},
		{
			name:     "complex mixed case matching reversed",
			parent:   ModTreePath{"foo-bar", "baz-qux"},
			child:    ModTreePath{"fooBar", "bazQux", "testCase"},
			expected: true,
		},
		{
			name:     "case mismatch but same structure",
			parent:   ModTreePath{"FooBar"},
			child:    ModTreePath{"fooBar", "child"},
			expected: true,
		},
		{
			name:     "case mismatch cli case",
			parent:   ModTreePath{"FooBar"},
			child:    ModTreePath{"foo-bar", "child"},
			expected: true,
		},
		{
			name:     "deeply nested with mixed cases",
			parent:   ModTreePath{"api"},
			child:    ModTreePath{"api", "myService", "get-user-info", "byId"},
			expected: true,
		},
		{
			name:     "mixed case mismatch - not parent",
			parent:   ModTreePath{"fooBar", "wrongSegment"},
			child:    ModTreePath{"foo-bar", "baz-qux", "test"},
			expected: false,
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			result := tc.parent.Contains(ctx, tc.child)
			require.Equal(s.T(), tc.expected, result, "parent: %v, child: %v", tc.parent, tc.child)
		})
	}
}

func (s *ModTreePathTestSuite) TestGlob(ctx context.Context, t *testctx.T) {
	testCases := []struct {
		name     string
		path     ModTreePath
		pattern  string
		expected bool
	}{
		{
			name:     "exact match api case",
			path:     ModTreePath{"foo", "bar", "baz"},
			pattern:  "foo:bar:baz",
			expected: true,
		},
		{
			name:     "exact match cli case",
			path:     ModTreePath{"fooBar", "bazQux"},
			pattern:  "foo-bar:baz-qux",
			expected: true,
		},
		{
			name:     "api case path with cli case pattern",
			path:     ModTreePath{"fooBar", "bazQux"},
			pattern:  "foo-bar:baz-qux",
			expected: true,
		},
		{
			name:     "cli case path with api case pattern",
			path:     ModTreePath{"foo-bar", "baz-qux"},
			pattern:  "fooBar:bazQux",
			expected: true,
		},
		{
			name:     "mixed case - api path segment with cli pattern segment",
			path:     ModTreePath{"fooBar", "baz"},
			pattern:  "foo-bar:baz",
			expected: true,
		},
		{
			name:     "mixed case - cli path segment with api pattern segment",
			path:     ModTreePath{"foo-bar", "qux"},
			pattern:  "fooBar:qux",
			expected: true,
		},
		{
			name:     "complex mixed case matching",
			path:     ModTreePath{"myService", "getUserInfo", "byId"},
			pattern:  "my-service:get-user-info:by-id",
			expected: true,
		},
		{
			name:     "complex mixed case matching reversed",
			path:     ModTreePath{"my-service", "get-user-info", "by-id"},
			pattern:  "myService:getUserInfo:byId",
			expected: true,
		},
		{
			name:     "wildcard single segment",
			path:     ModTreePath{"foo", "bar", "baz"},
			pattern:  "foo:*:baz",
			expected: true,
		},
		{
			name:     "wildcard multiple segments",
			path:     ModTreePath{"foo", "bar", "baz", "qux"},
			pattern:  "foo:**:qux",
			expected: true,
		},
		{
			name:     "wildcard at end",
			path:     ModTreePath{"foo", "bar", "baz"},
			pattern:  "foo:bar:*",
			expected: true,
		},
		{
			name:     "wildcard at beginning",
			path:     ModTreePath{"foo", "bar", "baz"},
			pattern:  "*:bar:baz",
			expected: true,
		},
		{
			name:     "double wildcard matches zero segments",
			path:     ModTreePath{"foo", "bar"},
			pattern:  "foo:**:bar",
			expected: true,
		},
		{
			name:     "double wildcard matches multiple segments",
			path:     ModTreePath{"foo", "a", "b", "c", "bar"},
			pattern:  "foo:**:bar",
			expected: true,
		},
		{
			name:     "wildcard with cli case path and api case pattern",
			path:     ModTreePath{"foo-bar", "baz-qux", "test"},
			pattern:  "fooBar:*:test",
			expected: true,
		},
		{
			name:     "wildcard with api case path and cli case pattern",
			path:     ModTreePath{"fooBar", "bazQux", "test"},
			pattern:  "foo-bar:*:test",
			expected: true,
		},
		{
			name:     "no match - different segment",
			path:     ModTreePath{"foo", "bar", "baz"},
			pattern:  "foo:wrong:baz",
			expected: false,
		},
		{
			name:     "no match - too few segments",
			path:     ModTreePath{"foo", "bar"},
			pattern:  "foo:bar:baz",
			expected: false,
		},
		{
			name:     "no match - too many segments",
			path:     ModTreePath{"foo", "bar", "baz", "extra"},
			pattern:  "foo:bar:baz",
			expected: false,
		},
		{
			name:     "wildcard with partial match",
			path:     ModTreePath{"fooBar", "bazQux"},
			pattern:  "foo*:*qux",
			expected: true,
		},
		{
			name:     "wildcard with partial match cli case",
			path:     ModTreePath{"foo-bar", "baz-qux"},
			pattern:  "foo*:*qux",
			expected: true,
		},
		{
			name:     "question mark wildcard",
			path:     ModTreePath{"foo", "bar"},
			pattern:  "fo?:ba?",
			expected: true,
		},
		{
			name:     "complex pattern with multiple wildcards",
			path:     ModTreePath{"service", "api", "v1", "users", "list"},
			pattern:  "service:**:users:*",
			expected: true,
		},
		{
			name:     "complex pattern with mixed cases",
			path:     ModTreePath{"myService", "api", "v1", "userInfo", "list"},
			pattern:  "my-service:**:user-info:*",
			expected: true,
		},
		{
			name:     "empty path with empty pattern",
			path:     ModTreePath{},
			pattern:  "",
			expected: true,
		},
		{
			name:     "single segment path",
			path:     ModTreePath{"foo"},
			pattern:  "foo",
			expected: true,
		},
		{
			name:     "single segment path cli case with api pattern",
			path:     ModTreePath{"foo-bar"},
			pattern:  "fooBar",
			expected: true,
		},
		{
			name:     "single segment path api case with cli pattern",
			path:     ModTreePath{"fooBar"},
			pattern:  "foo-bar",
			expected: true,
		},
		{
			name:     "case insensitive through api case conversion",
			path:     ModTreePath{"FooBar"},
			pattern:  "fooBar",
			expected: true,
		},
		{
			name:     "case insensitive through cli case conversion",
			path:     ModTreePath{"FooBar"},
			pattern:  "foo-bar",
			expected: true,
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			result, err := tc.path.Glob(ctx, tc.pattern)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.expected, result, "path: %v, pattern: %s", tc.path, tc.pattern)
		})
	}
}
