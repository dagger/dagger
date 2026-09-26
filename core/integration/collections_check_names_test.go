package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CollectionsSuite) TestGeneratorCheckNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name, receiver, path, query string
		flags                       []string
	}{
		{"module", "Collections", "", "", []string{"--collections"}},
		{"collection", "Item", "items/", "?item=a", []string{"--collections", "--collections-items-item=a"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			source := collectionGoSource + fmt.Sprintf(`
// +generate
func (*%s) Docs() *dagger.Changeset { panic("generator evaluated") }
// +generate
func (*%s) Clients() *dagger.Changeset { panic("generator evaluated") }
`, tc.receiver, tc.receiver)
			base := goGitBase(t, c).
				WithDirectory("/work", collectionSource(c).WithNewFile("collections/main.go", source)).
				WithWorkdir("/work")
			args := append([]string{"check", "-la", "-f=cli", "--check=stale"}, tc.flags...)
			out, err := base.With(daggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(out), "\n")
			require.Len(t, lines, 2)
			for i, generator := range []string{"clients", "docs"} {
				printed, _, _ := strings.Cut(lines[i], "#")
				want := append(append([]string{}, tc.flags...), "--check="+generator+"/stale")
				require.Equal(t, want, strings.Fields(printed))
				for _, command := range [][]string{{"check", "-la", "-f=link"}, {"list", "checks", "-a", "-f=link"}} {
					links, err := base.With(daggerExec(append(command, want...)...)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "dag+check://"+tc.path+generator+"/stale"+tc.query+"\n", links)
				}
			}
		})
	}
}
