package llb

import (
	"context"
	"testing"
	"time"

	"github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/require"
)

func TestAsyncNonBlocking(t *testing.T) {
	ctx := context.TODO()

	wait := make(chan struct{})
	ran := make(chan struct{})
	st := Image("alpine").Dir("/foo").Async(func(ctx context.Context, st State, c *Constraints) (State, error) {
		close(ran)
		<-wait // make sure callback doesn't block the chain
		return st.Run(Shlex("cmd1")).Dir("sub"), nil
	}).Run(Shlex("cmd2"))

	close(wait)

	select {
	case <-time.After(100 * time.Millisecond):
	case <-ran:
		require.Fail(t, "callback should not have been called")
	}

	def, err := st.Marshal(ctx)
	require.NoError(t, err)

	m, arr := parseDef(t, def.Def)
	require.Equal(t, 4, len(arr))

	dgst, idx := last(t, arr)
	require.Equal(t, 0, idx)
	require.Equal(t, m[dgst], arr[2])

	require.Equal(t, []string{"cmd1"}, arr[1].Op.(*pb.Op_Exec).Exec.Meta.Args)
	require.Equal(t, "/foo", arr[1].Op.(*pb.Op_Exec).Exec.Meta.Cwd)

	require.Equal(t, []string{"cmd2"}, arr[2].Op.(*pb.Op_Exec).Exec.Meta.Args)
	require.Equal(t, "/foo/sub", arr[2].Op.(*pb.Op_Exec).Exec.Meta.Cwd)
}
