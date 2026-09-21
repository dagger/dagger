package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// Capture must refuse the native operations' actual
// LazyState body latch. The body is paused before any filesystem work.
func TestCapturePersistedFilesystemDirectEvaluation(t *testing.T) {
	for _, family := range []string{"File", "Directory"} {
		t.Run(family, func(t *testing.T) {
			env := newPersistedFamiliesTestEnv(t, "filesystem-capture-guard")
			ctx, cache, srv := env.open(t)
			source := containerPersistenceTestDirectory("source-tree", "/")
			parent := env.attach(t, ctx, cache, srv, "source", source).(dagql.ObjectResult[*Directory])
			var value dagql.Typed
			var state *LazyState
			if family == "File" {
				file, err := source.Subfile(ctx, parent, "file")
				require.NoError(t, err)
				value, state = file, &file.Lazy.(*FileSubfileLazy).LazyState
			} else {
				dir, err := source.Subdirectory(ctx, parent, "subdir")
				require.NoError(t, err)
				value, state = dir, &dir.Lazy.(*DirectorySubdirectoryLazy).LazyState
			}
			child := env.attach(t, ctx, cache, srv, "child", value)
			entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				_ = state.Evaluate(ctx, family, func(context.Context) error {
					close(entered)
					<-finish
					return nil
				})
			}()
			<-entered
			record, err := cache.CapturePersistedRecord(ctx, child)
			close(finish)
			<-done
			t.Logf("capture while %s operation body latch held: err=%v payload=%s", family, err, record.Envelope.ObjectJSON)
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady, "live capture must refuse an active direct operation body")
		})
	}
}

func TestFilesystemOutputRevision(t *testing.T) {
	for _, family := range []string{"File", "Directory"} {
		t.Run(family, func(t *testing.T) {
			var output *filesystemOutput
			var version dagql.PersistedOutputVersion
			var path func(string)
			if family == "File" {
				file := &File{File: new(LazyAccessor[string, *File])}
				output, version, path = &file.filesystemOutput, file, file.SetPath
			} else {
				dir := &Directory{Dir: new(LazyAccessor[string, *Directory])}
				output, version, path = &dir.filesystemOutput, dir, dir.SetPath
			}
			before, err := version.PersistedOutputRevision()
			require.NoError(t, err)
			path("/completed")
			after, err := version.PersistedOutputRevision()
			require.NoError(t, err)
			require.Greater(t, after, before)
			output.outputMu.Lock()
			_, err = version.PersistedOutputRevision()
			output.outputMu.Unlock()
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
		})
	}
}

func TestFilesystemPersistenceRetainsBodyLatch(t *testing.T) {
	fileOp := &FileBlobLazy{LazyState: NewLazyState()}
	dirOp := &DirectorySubdirectoryLazy{LazyState: NewLazyState()}
	file := &File{Lazy: fileOp}
	dir := &Directory{Lazy: dirOp}
	for _, test := range []struct {
		name    string
		state   *LazyState
		clear   func()
		version dagql.PersistedOutputVersion
	}{{"File", &fileOp.LazyState, func() {
		file.outputMu.Lock()
		defer file.outputMu.Unlock()
		file.rememberBodyLocked(file.Lazy)
		file.Lazy = nil
	}, file}, {"Directory", &dirOp.LazyState, func() {
		dir.outputMu.Lock()
		defer dir.outputMu.Unlock()
		dir.rememberBodyLocked(dir.Lazy)
		dir.Lazy = nil
	}, dir}} {
		t.Run(test.name, func(t *testing.T) {
			test.state.LazyMu.Lock()
			test.clear()
			_, err := test.version.PersistedOutputRevision()
			test.state.LazyMu.Unlock()
			require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
			_, err = test.version.PersistedOutputRevision()
			require.NoError(t, err)
		})
	}
}
