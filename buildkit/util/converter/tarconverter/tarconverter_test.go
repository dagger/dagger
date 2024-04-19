package tarconverter

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	assert.NoError(t, tw.WriteHeader(&hdr))
	_, err := tw.Write(b)
	assert.NoError(t, err)
	assert.NoError(t, tw.Close())
	return buf.Bytes()
}

// https://github.com/moby/buildkit/pull/4057#issuecomment-1693484361
func TestPaddingForReader(t *testing.T) {
	inB := createTar(t, "foo", []byte("hi"))
	assert.Equal(t, 2048, len(inB))
	r := NewReader(bytes.NewReader(inB), func(hdr *tar.Header) {
		hdr.ModTime = time.Unix(0, 0)
	})
	outB, err := io.ReadAll(r)
	assert.NoError(t, err)
	assert.NoError(t, r.Close())
	assert.Equal(t, len(inB), len(outB))
}
