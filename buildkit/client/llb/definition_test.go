package llb

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestDefinitionEquivalence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
	}{
		{"scratch", Scratch()},
		{"image op", Image("ref")},
		{"exec op", Image("ref").Run(Shlex("args")).Root()},
		{"local op", Local("name")},
		{"git op", Git("remote", "ref")},
		{"http op", HTTP("url")},
		{"file op", Scratch().File(Mkdir("foo", 0600).Mkfile("foo/bar", 0600, []byte("data")).Copy(Scratch(), "src", "dst"))},
		{"platform constraint", Image("ref", LinuxArm64)},
		{"mount", Image("busybox").Run(Shlex(`sh -c "echo foo > /out/foo"`)).AddMount("/out", Scratch())},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.TODO()

			def, err := tc.state.Marshal(context.TODO())
			require.NoError(t, err)

			op, err := NewDefinitionOp(def.ToPB())
			require.NoError(t, err)

			err = op.Validate(ctx, nil)
			require.NoError(t, err)

			st2 := NewState(op.Output())

			def2, err := st2.Marshal(context.TODO())
			require.NoError(t, err)
			require.Equal(t, len(def.Def), len(def2.Def))
			require.Equal(t, len(def.Metadata), len(def2.Metadata))

			for i := 0; i < len(def.Def); i++ {
				res := bytes.Compare(def.Def[i], def2.Def[i])
				require.Equal(t, res, 0)
			}

			for dgst := range def.Metadata {
				require.Equal(t, def.Metadata[dgst], def2.Metadata[dgst])
			}

			expectedPlatform, err := tc.state.GetPlatform(ctx)
			require.NoError(t, err)
			actualPlatform, err := st2.GetPlatform(ctx)
			require.NoError(t, err)

			if expectedPlatform == nil && actualPlatform != nil {
				defaultPlatform := platforms.Normalize(platforms.DefaultSpec())
				expectedPlatform = &defaultPlatform
			}

			require.Equal(t, expectedPlatform, actualPlatform)
		})
	}
}

func TestDefinitionInputCache(t *testing.T) {
	src := HTTP("url")

	stA := Scratch().Run(
		Shlex("A"),
		AddMount("/mnt", src),
	)

	stB := Scratch().Run(
		Shlex("B"),
		AddMount("/mnt", src),
	)

	st := Scratch().Run(
		Shlex("args"),
		AddMount("/a", stA.Root()),
		AddMount("/a2", stA.GetMount("/mnt")),
		AddMount("/b", stB.Root()),
		AddMount("/b2", stB.GetMount("/mnt")),
	).Root()

	ctx := context.TODO()

	def, err := st.Marshal(context.TODO())
	require.NoError(t, err)

	op, err := NewDefinitionOp(def.ToPB())
	require.NoError(t, err)

	err = op.Validate(ctx, nil)
	require.NoError(t, err)

	st2 := NewState(op.Output())
	marshalDef := &Definition{
		Metadata: make(map[digest.Digest]pb.OpMetadata, 0),
	}
	constraints := &Constraints{}
	smc := newSourceMapCollector()

	// verify the expected number of vertexes gets marshalled
	vertexCache := make(map[Vertex]struct{})
	_, err = marshal(ctx, st2.Output().Vertex(ctx, constraints), marshalDef, smc, map[digest.Digest]struct{}{}, vertexCache, constraints)
	require.NoError(t, err)
	// 1 exec + 2x2 mounts from stA and stB + 1 src = 6 vertexes
	require.Equal(t, 6, len(vertexCache))

	// make sure that walking vertices in parallel doesn't cause panic
	var all []RunOption
	for i := 0; i < 100; i++ {
		var sts []RunOption
		for j := 0; j < 100; j++ {
			sts = append(sts, AddMount("/mnt", Scratch().Run(Shlex(fmt.Sprintf("%d-%d", i, j))).Root()))
		}
		all = append(all, AddMount("/mnt", Scratch().Run(append([]RunOption{Shlex("args")}, sts...)...).Root()))
	}
	def, err = Scratch().Run(append([]RunOption{Shlex("args")}, all...)...).Root().Marshal(context.TODO())
	require.NoError(t, err)
	op, err = NewDefinitionOp(def.ToPB())
	require.NoError(t, err)
	require.NoError(t, testParallelWalk(context.Background(), op.Output()))
}

func TestDefinitionNil(t *testing.T) {
	// should be an error, not a panic
	_, err := NewDefinitionOp(nil)
	require.Error(t, err)
}

func testParallelWalk(ctx context.Context, out Output) error {
	eg, egCtx := errgroup.WithContext(ctx)
	for _, o := range out.Vertex(ctx, nil).Inputs() {
		o := o
		eg.Go(func() error {
			return testParallelWalk(egCtx, o)
		})
	}
	return eg.Wait()
}
