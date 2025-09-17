package tarconverter

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTar(t testing.TB, name string, b []byte) []byte {
	buf := bytes.NewBuffer(nil)
	tw := tar.NewWriter(buf)
	hdr := tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Size:     int64(len(b)),
		Mode:     0o644,
		ModTime:  time.Now(),
	}
	require.NoError(t, tw.WriteHeader(&hdr))
	_, err := tw.Write(b)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// https://github.com/dagger/dagger/buildkit/pull/4057#issuecomment-1693484361
func TestPaddingForReader(t *testing.T) {
	inB := createTar(t, "foo", []byte("hi"))
	assert.Equal(t, 2048, len(inB))
	r := NewReader(bytes.NewReader(inB), func(hdr *tar.Header) {
		hdr.ModTime = time.Unix(0, 0)
	})
	outB, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, len(inB), len(outB))
}
