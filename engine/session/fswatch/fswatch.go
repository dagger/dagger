package fswatch

import (
	context "context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
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
	files   map[string]struct{}
	dirs    map[string]struct{}

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
			if !w.isWatched(event.Name) {
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
	if _, ok := w.files[path]; ok {
		return true
	}
	dir := filepath.Dir(path)
	if _, ok := w.dirs[dir]; ok {
		return true
	}
	return false
}

func (w *watcher) update(server FSWatch_WatchServer) error {
	for {
		req, err := server.Recv()
		if err != nil {
			return err
		}
		switch req := req.Request.(type) {
		case *WatchRequest_UpdateWatch:
			files, dirs := make(map[string]struct{}), make(map[string]struct{})
			newWatch := make(map[string]struct{}, len(req.UpdateWatch.Paths))
			for _, p := range req.UpdateWatch.Paths {
				if strings.HasSuffix(p, string(filepath.Separator)) {
					// directory, watch all the contents of the directory, and
					// allow all events in there
					p = filepath.Clean(p)
					newWatch[p] = struct{}{}
					dirs[p] = struct{}{}
				} else {
					// file, watch it's parent directory, and filter events
					// to only this file
					// see https://github.com/fsnotify/fsnotify#watching-a-file-doesnt-work-well
					p = filepath.Clean(p)
					newWatch[filepath.Dir(p)] = struct{}{}
					files[p] = struct{}{}
				}
			}

			if err := w.updateWatchers(newWatch); err != nil {
				return err
			}
			w.files, w.dirs = files, dirs
		}
	}
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
