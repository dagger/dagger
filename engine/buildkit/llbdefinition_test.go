package buildkit

import (
	"context"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestDefToDAG(t *testing.T) {
	ctx := context.Background()

	getState := func(echoMsg string) llb.State {
		imagePlusExec := llb.Image("alpine").Run(
			llb.Shlex("echo "+echoMsg+"1"),
			llb.AddMount("/emptymnt", llb.Scratch()),
			llb.AddMount("/somethingmnt", llb.Image("busybox")),
		)

		return llb.Image("debian").Run(
			llb.Shlex("echo "+echoMsg+"2"),
			llb.AddMount("/execmnt", imagePlusExec.Root()),
			llb.AddMount("/fromemptymnt", imagePlusExec.GetMount("/emptymnt")),
			llb.AddMount("/fromsomethingmnt", imagePlusExec.GetMount("/somethingmnt")),
		).Root()
	}

	llbdef, err := getState("a").Marshal(ctx)
	require.NoError(t, err)
	def := llbdef.ToPB()
	def.Source = nil

	dag, err := DefToDAG(def)
	require.NoError(t, err)

	require.NotNil(t, dag)
	_, isExec := dag.AsExec()
	require.False(t, isExec)
	require.Len(t, dag.Inputs, 1)

	execDag := dag.Inputs[0]
	exec, isExec := execDag.AsExec()
	require.True(t, isExec)
	require.NotNil(t, exec)
	require.Equal(t, []string{"echo", "a2"}, exec.Meta.Args)
	require.Len(t, exec.Inputs, 4) // 1 for rootfs and 3 mnts

	require.EqualValues(t, 0, exec.outputIndex)
	require.NotNil(t, exec.OutputMount())
	require.Equal(t, exec.OutputMount().Dest, "/")
	require.NotNil(t, exec.OutputMountBase())
	require.NotNil(t, exec.OutputMountBase().GetSource()) // rootfs is image source

	var rootfsMnt *OpDAG
	var execMnt, fromEmptyMnt, fromSomethingMnt *ExecOp
	for _, mnt := range exec.Mounts {
		dag := exec.Inputs[int(mnt.Input)]
		switch mnt.Dest {
		case "/":
			rootfsMnt = dag
		case "/execmnt":
			execMnt, isExec = dag.AsExec()
			require.True(t, isExec)
		case "/fromemptymnt":
			fromEmptyMnt, isExec = dag.AsExec()
			require.True(t, isExec)
		case "/fromsomethingmnt":
			fromSomethingMnt, isExec = dag.AsExec()
			require.True(t, isExec)
		default:
			require.Fail(t, "unexpected mount dest", mnt.Dest)
		}
	}

	require.NotNil(t, rootfsMnt)
	require.NotNil(t, rootfsMnt.GetSource())

	require.NotNil(t, execMnt)
	require.Equal(t, []string{"echo", "a1"}, execMnt.Meta.Args)
	require.Len(t, execMnt.Inputs, 2) // 1 for rootfs and 1 for non-scratch mnt
	require.EqualValues(t, 0, execMnt.outputIndex)
	require.Equal(t, "/", execMnt.OutputMount().Dest)
	require.NotNil(t, execMnt.OutputMountBase().GetSource())

	require.NotNil(t, fromEmptyMnt)
	require.Equal(t, []string{"echo", "a1"}, fromEmptyMnt.Meta.Args)
	require.Len(t, fromEmptyMnt.Inputs, 2)
	require.EqualValues(t, 1, fromEmptyMnt.outputIndex)
	require.Equal(t, "/emptymnt", fromEmptyMnt.OutputMount().Dest)
	require.Nil(t, fromEmptyMnt.OutputMountBase())

	require.NotNil(t, fromSomethingMnt)
	require.Equal(t, []string{"echo", "a1"}, fromSomethingMnt.Meta.Args)
	require.Len(t, fromSomethingMnt.Inputs, 2)
	require.EqualValues(t, 2, fromSomethingMnt.outputIndex)
	require.Equal(t, "/somethingmnt", fromSomethingMnt.OutputMount().Dest)
	require.NotNil(t, fromSomethingMnt.OutputMountBase().GetSource())

	t.Run("marshalling", func(t *testing.T) {
		remarshalledDef, err := dag.Marshal()
		require.NoError(t, err)
		defDgst := digest.FromBytes(def.Def[len(def.Def)-1])
		remarshalledDefDgst := digest.FromBytes(remarshalledDef.Def[len(remarshalledDef.Def)-1])
		require.Equal(t, defDgst, remarshalledDefDgst)
	})

	t.Run("modifications + marshalling", func(t *testing.T) {
		modifiedLLBDef, err := getState("b").Marshal(ctx)
		require.NoError(t, err)
		modifiedDef := modifiedLLBDef.ToPB()
		modifiedDef.Source = nil
		exec.Meta.Args = []string{"echo", "b2"}
		execMnt.Meta.Args = []string{"echo", "b1"}

		remarshalledDef, err := dag.Marshal()
		require.NoError(t, err)
		modifiedDefDgst := digest.FromBytes(modifiedDef.Def[len(modifiedDef.Def)-1])
		remarshalledDefDgst := digest.FromBytes(remarshalledDef.Def[len(remarshalledDef.Def)-1])
		require.Equal(t, modifiedDefDgst, remarshalledDefDgst)
	})

	t.Run("marshalling inputs", func(t *testing.T) {
		llbdef, err := getState("a").Marshal(ctx)
		require.NoError(t, err)
		def := llbdef.ToPB()
		def.Source = nil

		dag, err := DefToDAG(def)
		require.NoError(t, err)

		require.NotNil(t, dag)
		_, isExec := dag.AsExec()
		require.False(t, isExec)
		require.Len(t, dag.Inputs, 1)

		execDag := dag.Inputs[0]
		exec, isExec := execDag.AsExec()
		require.True(t, isExec)
		require.NotNil(t, exec)
		require.Equal(t, []string{"echo", "a2"}, exec.Meta.Args)
		require.Len(t, exec.Inputs, 4) // 1 for rootfs and 3 mnts

		inputDef, err := exec.Input(0).Marshal()
		require.NoError(t, err)
		require.Len(t, inputDef.Def, 2)

		dag, err = DefToDAG(inputDef)
		require.NoError(t, err)
		require.Len(t, dag.Inputs, 1)
		require.Equal(t, dag.Inputs[0], execDag.Inputs[0])
		require.Nil(t, dag.Op.Op)
	})
}
