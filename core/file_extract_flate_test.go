package core

import (
	"bytes"
	"compress/flate"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// A pooled zip decompressor that hit a corrupt entry decodes the next entry
// whole: closing it returns it to the pool, and the next entry resets it. The
// pool may hand out a fresh one instead (it drops some under the race
// detector), so the test retries until a failed one is reused.
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

	reused := false
	for range 100 {
		r := newZipFlateReader(bytes.NewReader(corrupt))
		_, err := io.ReadAll(r)
		require.Error(t, err)
		failed := r.(*pooledZipFlateReader).fr
		_ = r.Close()

		r = newZipFlateReader(bytes.NewReader(good.Bytes()))
		if r.(*pooledZipFlateReader).fr == failed {
			reused = true
		}
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.NoError(t, r.Close())
		if reused {
			break
		}
	}
	require.True(t, reused, "no decompressor that failed came back from the pool")
}
