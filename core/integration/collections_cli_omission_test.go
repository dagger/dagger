package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CollectionsSuite) TestCLISelectorOmission(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `package main
type Collections struct{}
func (*Collections) Items() *Items { return &Items{Names: []string{"a", "b"}} }
func (*Collections) Deferred() *Items { panic("collection keys evaluated") }
// +collection
type Items struct {
  // +keys
  Names []string
}
func (*Items) Get(key string) *Item { return &Item{} }
type Item struct{}
// +check
func (*Item) Run() error { panic("check evaluated") }
`
	base := goGitBase(t, c).
		WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).
		WithWorkdir("/work")
	for _, tc := range []struct {
		name, extra string
		omit        bool
		// typeKey is empty when the collection key identifies the only path.
		typeKey string
	}{
		{"one check per item", "", true, ""},
		{"hidden sibling check", `
// +check
func (*Item) Validate() error { panic("check evaluated") }
`, false, "run"},
		{"empty descendant collection", `
func (*Item) Parts() *Parts { return &Parts{Names: []string{}} }
// +collection
type Parts struct {
  // +keys
  Names []string
}
func (*Parts) Get(key string) *Part { return &Part{} }
type Part struct{}
// +check
func (*Part) Run() error { panic("check evaluated") }
`, false, "items/run"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := base.WithNewFile("collections/main.go", source+tc.extra)
			for _, command := range [][]string{{"check", "-l"}, {"list", "checks"}} {
				out, err := ctr.With(daggerExec(append(command, "-a", "items/run?collections-items-item=a", "-f=cli")...)).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "--collections --collections-items-item=a")
				require.Equal(t, !tc.omit, strings.Contains(out, "--check="), out)
				if tc.typeKey != "" {
					require.Contains(t, out, "--check="+tc.typeKey)
				}
				replay := append(append([]string{}, command...), "-a", "-f=link")
				prefix := "dagger check"
				if command[0] == "list" {
					prefix = "dagger list checks"
				}
				replay = append(replay, strings.Fields(collectionCLIArguments(t, out, prefix))...)
				links, err := ctr.With(daggerExec(replay...)).Stdout(ctx)
				require.NoError(t, err)
				// Deferred prevents a complete runtime proof. Keep the path even
				// when schema metadata can omit the CLI type selector.
				require.Equal(t, "dag+check://items/run?collections-items-item=a\n", links)
			}
			// The same proof works from schema metadata alone. The collection
			// constructor and both check functions must remain deferred.
			out, err := ctr.With(daggerExec("check", "-l", "deferred/run", "-f=cli")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "--collections --collections-deferred")
			require.Equal(t, !tc.omit, strings.Contains(out, "--check="), out)
		})
	}
}
