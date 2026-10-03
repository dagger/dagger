package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileCachePayloadBytes(t *testing.T) {
	t.Parallel()

	var nilFile *File
	require.Equal(t, int64(0), nilFile.CachePayloadBytes())

	file := &File{Lazy: &FileBlobLazy{LazyState: NewLazyState(), Filename: "schema.json", Contents: make([]byte, 1000)}}
	require.Equal(t, int64(1000), file.CachePayloadBytes())

	// A restored file keeps its saved operation bytes instead.
	restored := &File{Lazy: &FileRestoreLazy{LazyState: NewLazyState()}, lazyJSON: json.RawMessage(`{"contents":"AAAA"}`)}
	require.Equal(t, int64(19), restored.CachePayloadBytes())

	pending := &File{transferPending: &persistedFilePayload{LazyJSON: json.RawMessage(`{}`)}}
	require.Equal(t, int64(2), pending.CachePayloadBytes())

	require.Equal(t, int64(0), (&File{Lazy: &FileWithNameLazy{LazyState: NewLazyState(), Filename: "x"}}).CachePayloadBytes())
}

func TestDirectoryCachePayloadBytes(t *testing.T) {
	t.Parallel()

	var nilDir *Directory
	require.Equal(t, int64(0), nilDir.CachePayloadBytes())

	dir := &Directory{Lazy: &DirectoryWithNewFileLazy{LazyState: NewLazyState(), Dest: "a.txt", Content: make([]byte, 300)}}
	require.Equal(t, int64(300), dir.CachePayloadBytes())

	restored := &Directory{Lazy: &DirectoryRestoreLazy{LazyState: NewLazyState()}, lazyJSON: json.RawMessage(`{"content":"AAAA"}`)}
	require.Equal(t, int64(18), restored.CachePayloadBytes())
}

func TestJSONCachePayloadBytes(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(7), JSON(`{"a":1}`).CachePayloadBytes())
	require.Equal(t, int64(4), (&JSONValue{Data: []byte(`null`)}).CachePayloadBytes())
	require.Equal(t, int64(0), (*JSONValue)(nil).CachePayloadBytes())
}
