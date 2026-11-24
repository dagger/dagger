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
		name, receiver, linkFormat, exactPath, scope string
		flags                                        []string
	}{
		{"module", "Collections", "dag+check://?name=%s.stale\n", "", "", []string{"--collections"}},
		{"collection", "Item", "dag+check://items/%s/stale?collections-item=a\n", "dag+check://items/%[1]s/stale?type:Check=/items/%[1]s/stale", "items/**", []string{"--collections", "--collections-item=a"}},
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
			if tc.scope != "" {
				args = append(args, tc.scope)
			}
			out, err := base.With(daggerExec(args...)).Stdout(ctx)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(out), "\n")
			require.Len(t, lines, 2)
			for i, generator := range []string{"clients", "docs"} {
				printed, _, _ := strings.Cut(lines[i], "#")
				want := append(append([]string{}, tc.flags...), "--name="+generator+".stale")
				if tc.exactPath != "" {
					want = append([]string{fmt.Sprintf(tc.exactPath, generator)}, want...)
				}
				require.Equal(t, want, collectionCLIFields(t, strings.TrimSpace(printed)+"\n", "dagger check"))
				for _, command := range [][]string{{"check", "-la", "-f=link"}, {"list", "checks", "-a", "-f=link"}} {
					links, err := base.With(daggerExec(append(command, want...)...)).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, fmt.Sprintf(tc.linkFormat, generator), links)
				}
			}
		})
	}
}
