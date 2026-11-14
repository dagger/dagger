package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/shell"
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
	}{
		{"one check per item", ""},
		{"hidden sibling check", `
// +check
func (*Item) Validate() error { panic("check evaluated") }
`},
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
`},
		{"populated descendant collection", `
func (*Item) Parts() *Parts { return &Parts{Names: []string{"child"}} }
// +collection
type Parts struct {
  // +keys
  Names []string
}
func (*Parts) Get(key string) *Part { return &Part{} }
type Part struct{}
// +check
func (*Part) Run() error { panic("check evaluated") }
`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := base.WithNewFile("collections/main.go", source+tc.extra)
			for _, command := range [][]string{{"check", "-l"}, {"list", "checks"}} {
				out, err := ctr.With(daggerExec(append(command, "-a", "items/run?CollectionsItems=a", "-f=cli")...)).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "--collections-item=a")

				require.Contains(t, out, "--name=items.run")
				replay := append(append([]string{}, command...), "-a", "-f=link")
				prefix := "dagger check"
				if command[0] == "list" {
					prefix = "dagger list checks"
				}
				selectors, err := shell.Fields(collectionCLIArguments(t, out, prefix), nil)
				require.NoError(t, err)
				replay = append(replay, selectors...)
				links, err := ctr.With(daggerExec(replay...)).Stdout(ctx)
				require.NoError(t, err)
				require.NotEmpty(t, strings.TrimSpace(links))
				direct, err := ctr.With(daggerExec(append(append([]string{}, command...), "-a", "items/run?CollectionsItems=a", "-f=link")...)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, direct, links)
				replayed, err := ctr.With(daggerExec(append(append([]string{}, command...), "-a", strings.TrimSpace(links), "-f=link")...)).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, links, replayed)
			}
			// The same proof works from schema metadata alone. The collection
			// constructor and both check functions must remain deferred.
			out, err := ctr.With(daggerExec("check", "-l", "deferred/run", "-f=cli")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "--name=deferred.run")
		})
	}
}
