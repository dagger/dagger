package stack

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func testcall1() error {
	return errors.Errorf("error1")
}

func testcall2() error {
	return errors.WithStack(testcall1())
}

func testcall3() error {
	err := testcall2()
	// this is from a different line
	return errors.WithStack(err)
}

func TestCompressStacks(t *testing.T) {
	err := testcall2()
	st := Traces(err)

	// full trace match, shorter is removed
	require.Len(t, st, 1)
	require.GreaterOrEqual(t, len(st[0].Frames), 2)

	f := st[0].Frames
	require.Contains(t, f[0].Name, "testcall1")
	require.Contains(t, f[1].Name, "testcall2")
}

func TestCompressMultiStacks(t *testing.T) {
	err := testcall3()
	st := Traces(err)

	require.Len(t, st, 2)
	require.GreaterOrEqual(t, len(st[0].Frames), 4)

	f1 := st[0].Frames
	require.Contains(t, f1[0].Name, "testcall1")
	require.Contains(t, f1[1].Name, "testcall2")
	require.Contains(t, f1[2].Name, "testcall3")

	f2 := st[1].Frames
	require.Contains(t, f2[0].Name, "testcall3")
	// next line is shared and everything after is removed
	require.Len(t, f2, 2)
	require.Equal(t, f1[3], f2[1])
}
