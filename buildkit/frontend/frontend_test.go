package frontend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
)

func init() {
	if workers.IsTestDockerd() {
		workers.InitDockerdWorker()
	} else {
		workers.InitOCIWorker()
		workers.InitContainerdWorker()
	}
}

func TestFrontendIntegration(t *testing.T) {
	integration.Run(t, integration.TestFuncs(
		testRefReadFile,
		testRefReadDir,
		testRefStatFile,
		testRefEvaluate,
		testReturnNil,
	))
}

func testReturnNil(t *testing.T, sb integration.Sandbox) {
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		return nil, nil
	}

	_, err = c.Build(ctx, client.SolveOpt{}, "", frontend, nil)
	require.NoError(t, err)

	frontend = func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		return gateway.NewResult(), nil
	}

	_, err = c.Build(ctx, client.SolveOpt{}, "", frontend, nil)
	require.NoError(t, err)
}

func testRefReadFile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	testcontent := []byte(`foobar`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("test", testcontent, 0666),
	)

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		def, err := llb.Local("mylocal").Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		for _, tc := range []struct {
			name string
			exp  []byte
			r    *gateway.FileRange
		}{
			{"fullfile", testcontent, nil},
			{"prefix", []byte(`foo`), &gateway.FileRange{Offset: 0, Length: 3}},
			{"suffix", []byte(`ar`), &gateway.FileRange{Offset: 4, Length: 2}},
			{"mid", []byte(`oba`), &gateway.FileRange{Offset: 2, Length: 3}},
			{"overrun", []byte(`bar`), &gateway.FileRange{Offset: 3, Length: 10}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r, err := ref.ReadFile(ctx, gateway.ReadRequest{
					Filename: "test",
					Range:    tc.r,
				})
				require.NoError(t, err)
				assert.Equal(t, tc.exp, r)
			})
		}

		return gateway.NewResult(), nil
	}

	_, err = c.Build(ctx, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			"mylocal": dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)
}

func testRefReadDir(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	dir := integration.Tmpdir(
		t,
		fstest.CreateDir("somedir", 0777),
		fstest.CreateFile("somedir/foo1.txt", []byte(`foo1`), 0666),
		fstest.CreateFile("somedir/foo2.txt", []byte{}, 0666),
		fstest.CreateFile("somedir/bar.log", []byte(`somethingsomething`), 0666),
		fstest.Symlink("bar.log", "somedir/link.log"),
		fstest.CreateDir("somedir/baz.dir", 0777),
	)

	expMap := make(map[string]*fstypes.Stat)

	fsutil.Walk(ctx, dir.Name, nil, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		stat, ok := info.Sys().(*fstypes.Stat)
		require.True(t, ok)
		stat.ModTime = 0                     // this will inevitably differ, we clear it during the tests below too
		stat.Path = filepath.Base(stat.Path) // we are only testing reading a single directory here
		expMap[filepath.ToSlash(path)] = stat
		return nil
	})

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		def, err := llb.Local("mylocal").Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		for _, tc := range []struct {
			name string
			req  gateway.ReadDirRequest
			exp  []*fstypes.Stat
		}{
			{
				name: "toplevel",
				req:  gateway.ReadDirRequest{Path: "/"},
				exp: []*fstypes.Stat{
					expMap["somedir"],
				},
			},
			{
				name: "subdir",
				req:  gateway.ReadDirRequest{Path: "/somedir"},
				exp: []*fstypes.Stat{
					expMap["somedir/bar.log"],
					expMap["somedir/baz.dir"],
					expMap["somedir/foo1.txt"],
					expMap["somedir/foo2.txt"],
					expMap["somedir/link.log"],
				},
			},
			{
				name: "globtxt",
				req:  gateway.ReadDirRequest{Path: "/somedir", IncludePattern: "*.txt"},
				exp: []*fstypes.Stat{
					expMap["somedir/foo1.txt"],
					expMap["somedir/foo2.txt"],
				},
			},
			{
				name: "globlog",
				req:  gateway.ReadDirRequest{Path: "/somedir", IncludePattern: "*.log"},
				exp: []*fstypes.Stat{
					expMap["somedir/bar.log"],
					expMap["somedir/link.log"],
				},
			},
			{
				name: "subsubdir",
				req:  gateway.ReadDirRequest{Path: "/somedir", IncludePattern: "*.dir"},
				exp: []*fstypes.Stat{
					expMap["somedir/baz.dir"],
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dirents, err := ref.ReadDir(ctx, tc.req)
				require.NoError(t, err)
				for _, s := range dirents {
					s.ModTime = 0 // this will inevitably differ, we cleared it in the expected versions above.
				}
				assert.Equal(t, tc.exp, dirents)
			})
		}

		return gateway.NewResult(), nil
	}

	_, err = c.Build(ctx, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			"mylocal": dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)
}

func testRefStatFile(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	testcontent := []byte(`foobar`)

	dir := integration.Tmpdir(
		t,
		fstest.CreateFile("test", testcontent, 0666),
	)

	exp, err := fsutil.Stat(filepath.Join(dir.Name, "test"))
	require.NoError(t, err)

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		def, err := llb.Local("mylocal").Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		st, err := ref.StatFile(ctx, gateway.StatRequest{
			Path: "test",
		})
		require.NoError(t, err)
		require.NotNil(t, st)
		assert.Equal(t, exp, st)
		return gateway.NewResult(), nil
	}

	_, err = c.Build(ctx, client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			"mylocal": dir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)
}

func testRefEvaluate(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	ctx := sb.Context()

	c, err := client.New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		st := llb.Scratch().File(llb.Mkfile("/test", 0666, []byte{}))
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, err
		}
		res, err := c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		st = llb.Scratch().File(llb.Mkfile("/test/dir-does-not-exist", 0666, []byte{}))
		def, err = st.Marshal(ctx)
		if err != nil {
			return nil, err
		}
		res, err = c.Solve(ctx, gateway.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref2, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		require.NoError(t, ref.Evaluate(ctx))
		require.Error(t, ref2.Evaluate(ctx))
		return gateway.NewResult(), nil
	}

	_, err = c.Build(ctx, client.SolveOpt{}, "", frontend, nil)
	require.NoError(t, err)
}
