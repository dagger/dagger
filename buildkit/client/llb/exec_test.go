package llb

import (
	"context"
	"testing"

	"github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

func TestTmpfsMountError(t *testing.T) {
	t.Parallel()

	st := Image("foo").Run(Shlex("args")).AddMount("/tmp", Scratch(), Tmpfs())
	_, err := st.Marshal(context.TODO())

	require.Error(t, err)
	require.Contains(t, err.Error(), "can't be used as a parent")

	st = Image("foo").Run(Shlex("args"), AddMount("/tmp", Scratch(), Tmpfs())).Root()
	_, err = st.Marshal(context.TODO())
	require.NoError(t, err)

	st = Image("foo").Run(Shlex("args"), AddMount("/tmp", Image("bar"), Tmpfs())).Root()
	_, err = st.Marshal(context.TODO())
	require.Error(t, err)
	require.Contains(t, err.Error(), "must use scratch")
}

func TestValidGetMountIndex(t *testing.T) {
	// tests for https://github.com/moby/buildkit/issues/1520

	// tmpfs mount /c will sort later than target mount /b, /b will have output index==1
	st := Image("foo").Run(Shlex("args"), AddMount("/b", Scratch()), AddMount("/c", Scratch(), Tmpfs())).GetMount("/b")

	mountOutput, ok := st.Output().(*output)
	require.True(t, ok, "mount output is expected type")

	mountIndex, err := mountOutput.getIndex()
	require.NoError(t, err, "failed to getIndex")
	require.Equal(t, pb.OutputIndex(1), mountIndex, "unexpected mount index")

	// now swapping so the tmpfs mount /a will sort earlier than the target mount /b, /b should still have output index==1
	st = Image("foo").Run(Shlex("args"), AddMount("/b", Scratch()), AddMount("/a", Scratch(), Tmpfs())).GetMount("/b")

	mountOutput, ok = st.Output().(*output)
	require.True(t, ok, "mount output is expected type")

	mountIndex, err = mountOutput.getIndex()
	require.NoError(t, err, "failed to getIndex")
	require.Equal(t, pb.OutputIndex(1), mountIndex, "unexpected mount index")
}

func TestExecOpMarshalConsistency(t *testing.T) {
	var prevDef [][]byte
	st := Image("busybox:latest").
		Run(
			Args([]string{"ls /a; ls /b"}),
			AddMount("/b", Scratch().File(Mkfile("file1", 0644, []byte("file1 contents")))),
		).AddMount("/a", Scratch().File(Mkfile("file2", 0644, []byte("file2 contents"))))

	for i := 0; i < 100; i++ {
		def, err := st.Marshal(context.TODO())
		require.NoError(t, err)

		if prevDef != nil {
			require.Equal(t, def.Def, prevDef)
		}

		prevDef = def.Def
	}
}
