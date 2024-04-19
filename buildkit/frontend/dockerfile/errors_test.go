package dockerfile

import (
	"fmt"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

func testErrorsSourceMap(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	f := getFrontend(t, sb)

	tcases := []struct {
		name       string
		dockerfile string
		errorLine  []int
	}{
		{
			name: "invalidenv",
			dockerfile: `from alpine
env`,
			errorLine: []int{2},
		},
		{
			name: "invalidsyntax",
			dockerfile: `#syntax=foobar
from alpine`,
			errorLine: []int{1},
		},
		{
			name: "invalidrun",
			dockerfile: `from scratch
env foo=bar
run what`,
			errorLine: []int{3},
		},
		{
			name: "invalidcopy",
			dockerfile: `from scratch
env foo=bar
copy foo bar
env bar=baz`,
			errorLine: []int{3},
		},
		{
			name: "invalidflag",
			dockerfile: `from scratch
env foo=bar
copy --foo=bar / /
env bar=baz`,
			errorLine: []int{3},
		},
		{
			name: "invalidcopyfrom",
			dockerfile: `from scratch
env foo=bar
copy --from=invalid foo bar
env bar=baz`,
			errorLine: []int{3},
		},
		{
			name: "invalidmultiline",
			dockerfile: `from scratch
run wh\
at
env bar=baz`,
			errorLine: []int{2, 3},
		},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			dir := integration.Tmpdir(
				t,
				fstest.CreateFile("Dockerfile", []byte(tc.dockerfile), 0600),
			)

			c, err := client.New(sb.Context(), sb.Address())
			require.NoError(t, err)
			defer c.Close()

			_, err = f.Solve(sb.Context(), c, client.SolveOpt{
				LocalMounts: map[string]fsutil.FS{
					dockerui.DefaultLocalNameDockerfile: dir,
					dockerui.DefaultLocalNameContext:    dir,
				},
			}, nil)
			require.Error(t, err)

			srcs := errdefs.Sources(err)
			require.Equal(t, 1, len(srcs))

			require.Equal(t, "Dockerfile", srcs[0].Info.Filename)
			require.Equal(t, "Dockerfile", srcs[0].Info.Language)
			require.Equal(t, tc.dockerfile, string(srcs[0].Info.Data))
			require.Equal(t, len(tc.errorLine), len(srcs[0].Ranges))
			require.NotNil(t, srcs[0].Info.Definition)

		next:
			for _, l := range tc.errorLine {
				for _, l2 := range srcs[0].Ranges {
					if l2.Start.Line == int32(l) {
						continue next
					}
				}
				require.Fail(t, fmt.Sprintf("line %d not found", l))
			}
		})
	}
}
