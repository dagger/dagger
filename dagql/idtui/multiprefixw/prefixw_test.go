package multiprefixw

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrefixedWriter_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[web] "
	_, err := pw.Write([]byte("hello world\n"))
	require.NoError(t, err)
	require.Equal(t, "[web] hello world\n", buf.String())
}

func TestPrefixedWriter_PrefixSwitch(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[web] "
	_, err := pw.Write([]byte("hello\n"))
	require.NoError(t, err)
	pw.Prefix = "[db] "
	_, err = pw.Write([]byte("querying\n"))
	require.NoError(t, err)
	expected := "[web] hello\n[db] querying\n"
	require.Equal(t, expected, buf.String())
}

func TestPrefixedWriter_MultiLineSinglePrefix(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[worker] "
	_, err := pw.Write([]byte("first line\nsecond line\n"))
	require.NoError(t, err)
	require.Equal(t, "[worker] first line\n[worker] second line\n", buf.String())
}

func TestPrefixedWriter_NoNewlineBufferingAndSwitch(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[A] "
	_, err := pw.Write([]byte("partial"))
	require.NoError(t, err)
	_, err = pw.Write([]byte(" line"))
	require.NoError(t, err)
	pw.Prefix = "[B] "
	_, err = pw.Write([]byte("other\n"))
	require.NoError(t, err)

	expected := "[A] partial line\u23CE\n[B] other\n"
	require.Equal(t, expected, buf.String())
}

func TestPrefixedWriter_MultipleSwitches(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[x] "
	_, err := pw.Write([]byte("foo\nbar"))
	require.NoError(t, err)
	pw.Prefix = "[y] "
	_, err = pw.Write([]byte("baz\nqux"))
	require.NoError(t, err)
	expected :=
		"[x] foo\n[x] bar\u23CE\n[y] baz\n[y] qux"
	require.Equal(t, expected, buf.String())
}

func TestPrefixedWriter_HeaderPrefix(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	header := "== HEADER ==\n"
	pw.Prefix = header
	_, err := pw.Write([]byte("line1\nline2\n"))
	require.NoError(t, err)
	expected := "== HEADER ==\nline1\nline2\n"
	require.Equal(t, expected, buf.String())
}

func TestPrefixedWriter_HeaderPrefixSwitchToNormal(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "## Welcome ##\n"
	_, err := pw.Write([]byte("first\n"))
	require.NoError(t, err)
	pw.Prefix = "[info] "
	_, err = pw.Write([]byte("second\n"))
	require.NoError(t, err)
	expected := "## Welcome ##\nfirst\n\n[info] second\n"
	require.Equal(t, expected, buf.String())
}

func TestPrefixedWriter_HeaderPrefixSwitchFromPartialLine(t *testing.T) {
	var buf bytes.Buffer
	pw := New(&buf)

	pw.Prefix = "[tag] "
	_, err := pw.Write([]byte("no newline"))
	require.NoError(t, err)
	pw.Prefix = "### HEADER ###\n"
	_, err = pw.Write([]byte("newblock\n"))
	require.NoError(t, err)
	expected := "[tag] no newline\u23CE\n\n### HEADER ###\nnewblock\n"
	require.Equal(t, expected, buf.String())
}
