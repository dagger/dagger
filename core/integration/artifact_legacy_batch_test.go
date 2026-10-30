package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ArtifactsSuite) TestLegacyCollectionCheckBatching(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name, version, filter, selected string
		count                           int
	}{
		{"old full selection", "v1.0.0-beta.21", "", "a,b", 1},
		{"old one item", "v1.0.0-beta.21", `filterDimensionKeys(dimension: "/view/tests", keys: ["b"])`, "b", 1},
		{"old exclusion", "v1.0.0-beta.21", `withoutUri(uri: "dag+check://?view-case=a")`, "b", 1},
		{"old all excluded", "v1.0.0-beta.21", `withoutUri(uri: "dag+check://?view-case=a&view-case=b")`, "", 0},
		{"new full selection", engine.CollectionItemChecksVersion, "", "a,b", 3},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			itemBody := `raise "legacy item check must not run"`
			if tc.count == 3 {
				itemBody = "null"
			}
			source := fmt.Sprintf(`type View {
  tests: Cases! { Cases(names: ["a", "b"]) }
}
type Cases @collection {
  pub names: [String!]! @keys
  pub delta: CollectionDelta @delta
  lookup(name: String!): Case! @get { Case(name: name) }
  @cache(policy: FunctionCachePolicy.Never)
  test: Void @check {
    if (names.join(",") != %q) { raise "wrong batch: " + names.join(",") }
    null
  }
}
type Case {
  pub name: String!
  @cache(policy: FunctionCachePolicy.Never)
  test: Void @check { %s }
}`, tc.selected, itemBody)
			base := artifactViewWorkspace(t, c, source).
				WithNewFile("view/dagger-module.toml", fmt.Sprintf("name = \"view\"\nengineVersion = %q\n[runtime]\nsource = \"dang\"\n", tc.version))
			selection := `filterTypes(types: ["Check"])`
			query := `asChecks { sync { pass error { message } } }`
			if tc.filter != "" {
				query = tc.filter + " { " + query + " }"
			}
			query = `{ currentWorkspace { artifacts { ` + selection + " { " + query + " } } } }"
			out, err := base.With(daggerQuery(query)).Stdout(ctx)
			require.NoError(t, err, out)
			require.NotContains(t, out, `"pass":false`, out)
			require.Equal(t, tc.count, len(regexp.MustCompile(`"pass"\s*:\s*true`).FindAllString(out, -1)), out)
			// The CLI must use the same execution plan as asChecks.
			if tc.filter == "" {
				out, err = base.With(daggerExec("check", "--check=test")).CombinedOutput(ctx)
				require.NoError(t, err, out)
			}
		})
	}
}

func (ArtifactsSuite) TestLegacyCollectionGeneratorBatching(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name, version, args string
		files                []string
		staleChecks          int
	}{
		{"old full selection", "v1.0.0-beta.21", "", []string{"batch-a-b-c.txt"}, 1},
		{"old one item", "v1.0.0-beta.21", "--view-pkg=b", []string{"batch-b.txt"}, 1},
		{"new full selection", engine.CollectionItemChecksVersion, "", []string{"batch-a-b-c.txt", "item-a.txt", "item-b.txt", "item-c.txt"}, 4},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			source := `type View {
  packages: Pkgs! { Pkgs(names: ["a", "b", "c"]) }
}
type Pkgs @collection {
  pub names: [String!]! @keys
  lookup(name: String!): Pkg! @get { Pkg(name: name) }
  generate: Changeset! @generate {
    directory.withNewFile("batch-" + names.join("-") + ".txt", "x").changes(directory)
  }
}
type Pkg {
  pub name: String!
  generate: Changeset! @generate {
    directory.withNewFile("item-" + name + ".txt", "x").changes(directory)
  }
}`
			base := artifactViewWorkspace(t, c, source).
				WithNewFile("view/dagger-module.toml", fmt.Sprintf("name = \"view\"\nengineVersion = %q\n[runtime]\nsource = \"dang\"\n", tc.version))
			args := []string{"generate", "-y"}
			if tc.args != "" {
				args = append(args, tc.args)
			}
			generated := base.With(daggerExec(args...))
			out, err := generated.CombinedOutput(ctx)
			require.NoError(t, err, out)
			entries, err := generated.Directory(".").Entries(ctx)
			require.NoError(t, err)
			var files []string
			for _, entry := range entries {
				if strings.HasPrefix(entry, "batch-") || strings.HasPrefix(entry, "item-") {
					files = append(files, entry)
				}
			}
			require.ElementsMatch(t, tc.files, files, out)
			if tc.args == "" {
				// Stale checks run their generator, so they follow the same plan.
				out, err = base.With(daggerQuery(`{ currentWorkspace { artifacts { filterTypes(types: ["Check"]) { asChecks { sync { pass } } } } } }`)).Stdout(ctx)
				require.NoError(t, err, out)
				require.Equal(t, tc.staleChecks, len(regexp.MustCompile(`"pass"\s*:`).FindAllString(out, -1)), out)
			}
		})
	}
}
