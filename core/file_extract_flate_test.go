package core

import (
	"bytes"
	"compress/flate"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// A pooled zip decompressor that hit a corrupt entry decodes the next entry
// whole: closing it returns it to the pool, and the next entry resets it.
func TestZipFlateReaderReusedAfterCorruptEntry(t *testing.T) {
	want := bytes.Repeat([]byte("every entry decodes whole "), 4096)
	var good bytes.Buffer
	w, err := flate.NewWriter(&good, flate.DefaultCompression)
	require.NoError(t, err)
	_, err = w.Write(want)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	corrupt := bytes.Clone(good.Bytes()[:good.Len()/2])
	corrupt[len(corrupt)/2] ^= 0xff

	for range 3 {
		r := newZipFlateReader(bytes.NewReader(corrupt))
		_, err := io.ReadAll(r)
		require.Error(t, err)
		_ = r.Close()

		r = newZipFlateReader(bytes.NewReader(good.Bytes()))
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.NoError(t, r.Close())
	}
}
