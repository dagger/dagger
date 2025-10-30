package fswatch

import (
	context "context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagger/dagger/util/fsxutil"
	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/fsnotify/fsnotify"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
	grpc "google.golang.org/grpc"
)

type FSWatcherAttachable struct {
	UnimplementedFSWatchServer
}

var _ FSWatchServer = FSWatcherAttachable{}

func (f FSWatcherAttachable) Register(srv *grpc.Server) {
	RegisterFSWatchServer(srv, f)
}

func (f FSWatcherAttachable) Watch(server FSWatch_WatchServer) error {
	fswatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fswatcher.Close()

	t := time.NewTimer(0)
	if !t.Stop() {
		<-t.C // Drain if needed
	}
	w := &watcher{
		watcher:     fswatcher,
		flushEvents: t,
		flushDelay:  time.Second,
	}

	eg, ctx := errgroup.WithContext(server.Context())
	eg.Go(func() error {
		return w.watch(ctx)
	})
	eg.Go(func() error {
		return w.stream(ctx, server)
	})
	eg.Go(func() error {
		return w.update(server)
	})
	return eg.Wait()
}

type watcher struct {
	watcher *fsnotify.Watcher

	path      string
	include   []string
	exclude   []string
	gitignore bool

	dirs map[string]struct{}

	includeMatcher   *patternmatcher.PatternMatcher
	excludeMatcher   *patternmatcher.PatternMatcher
	gitignoreMatcher *fsxutil.GitignoreMatcher

	flushDelay     time.Duration
	flushEvents    *time.Timer
	bufferedEvents []*FileEvent
	eventsMu       sync.Mutex
}

func (w *watcher) watch(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}

			relPath, err := filepath.Rel(w.path, event.Name)
			if err != nil {
				slog.Info("fsnotify path error", "error", err)
				continue
			}
			if !w.isWatched(relPath) {
				continue
			}

			// XXX: it seems possible that we'd lose events if we create files
			// in directories that are not yet watched
			//
			// - we could avoid this by setting up a pre-emptive watch on created directories
			// - or we could stat the directory immediately upon updating the
			//   watch list, and immediately trigger an event
			// - *or* we could provide the git/include/exclude style filtering
			//   options, and update our own watch lists. with some cleverness,
			//   we should be able to make sure that we can include all fs events

			w.eventsMu.Lock()
			w.bufferedEvents = append(w.bufferedEvents, toWatchEvent(event))
			w.flushEvents.Reset(w.flushDelay)
			w.eventsMu.Unlock()
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			slog.Info("fsnotify error", "error", err)
			return err
		}
	}
}

func (w *watcher) isWatched(path string) bool {
	if w.includeMatcher != nil {
		matched, err := w.includeMatcher.MatchesOrParentMatches(path)
		if err != nil {
			slog.Info("include pattern match error", "error", err)
			return false
		}
		if !matched {
			return false
		}
	}
	if w.excludeMatcher != nil {
		matched, err := w.excludeMatcher.MatchesOrParentMatches(path)
		if err != nil {
			slog.Info("exclude pattern match error", "error", err)
			return false
		}
		if matched {
			return false
		}
	}
	if w.gitignoreMatcher != nil {
		_, isDir := w.dirs[filepath.Clean(path)]
		matched, err := w.gitignoreMatcher.Matches(path, isDir)
		if err != nil {
			slog.Info("gitignore pattern match error", "error", err)
			return false
		}
		if matched {
			return false
		}
	}

	return true
}

func (w *watcher) update(server FSWatch_WatchServer) error {
	for {
		req, err := server.Recv()
		if err != nil {
			return err
		}
		switch req := req.Request.(type) {
		case *WatchRequest_UpdateWatch:
			w.path = req.UpdateWatch.Path
			w.gitignore = req.UpdateWatch.Gitignore
			w.include = req.UpdateWatch.Include
			w.exclude = req.UpdateWatch.Exclude

			err := w.updateWatch(server.Context())
			if err != nil {
				return err
			}
		}
	}
}

func (w *watcher) updateWatch(ctx context.Context) error {
	newWatch := make(map[string]struct{})
	f, err := fsutil.NewFS(w.path)
	if err != nil {
		return err
	}
	if w.gitignore {
		w.gitignoreMatcher = fsxutil.NewGitIgnoreMatcher(f)
		f, err = fsxutil.NewGitIgnoreFS(f, w.gitignoreMatcher)
		if err != nil {
			return err
		}
	}
	f, err = fsutil.NewFilterFS(f, &fsutil.FilterOpt{
		IncludePatterns: w.include,
		ExcludePatterns: w.exclude,
	})
	if err != nil {
		return err
	}
	if len(w.include) > 0 {
		w.includeMatcher, err = patternmatcher.New(w.include)
		if err != nil {
			return err
		}
	}
	if len(w.exclude) > 0 {
		w.excludeMatcher, err = patternmatcher.New(w.exclude)
		if err != nil {
			return err
		}
	}

	newWatch[filepath.Clean(w.path)] = struct{}{}
	err = f.Walk(ctx, "/", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			newWatch[filepath.Join(w.path, path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}

	w.dirs = newWatch

	return w.updateWatchers(newWatch)
}

func (w *watcher) updateWatchers(newWatch map[string]struct{}) error {
	current := w.watcher.WatchList()
	oldWatch := make(map[string]struct{}, len(current))
	for _, path := range current {
		oldWatch[path] = struct{}{}
	}

	// add all paths not currently being watched
	for path := range newWatch {
		if _, ok := oldWatch[path]; !ok {
			err := w.watcher.Add(path)
			if err != nil {
				return err
			}
		}
	}

	// remove all paths that are currently being watched but not in the new list
	for path := range oldWatch {
		if _, ok := newWatch[path]; !ok {
			err := w.watcher.Remove(path)
			if err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
				return err
			}
		}
	}

	return nil
}

func (w *watcher) stream(ctx context.Context, server FSWatch_WatchServer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.flushEvents.C:
			w.eventsMu.Lock()
			events := WatchResponse_FileEvents{
				FileEvents: &FileEvents{
					Events: w.bufferedEvents,
				},
			}
			w.bufferedEvents = nil
			w.eventsMu.Unlock()

			res := &WatchResponse{Event: &events}
			err := server.Send(res)
			if err != nil {
				return err
			}

			// TODO: this probably ain't the right place
			err = w.updateWatch(ctx)
			if err != nil {
				return err
			}
		}
	}
}

func toWatchEvent(event fsnotify.Event) *FileEvent {
	var op FileEventType
	switch {
	case event.Has(fsnotify.Create):
		op = CREATE
	case event.Has(fsnotify.Write):
		op = WRITE
	case event.Has(fsnotify.Remove):
		op = REMOVE
	case event.Has(fsnotify.Rename):
		op = RENAME
	case event.Has(fsnotify.Chmod):
		op = CHMOD
	}
	return &FileEvent{
		Path: event.Name,
		Type: op,
	}
}
