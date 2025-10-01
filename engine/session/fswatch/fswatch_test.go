package fswatch

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

type mockFSWatchServer struct {
	grpc.ServerStream

	ctx context.Context

	requests   []*WatchRequest
	requestsCh chan *WatchRequest

	responses []*WatchResponse
}

func newMockServer(ctx context.Context, reqs ...*WatchRequest) *mockFSWatchServer {
	return &mockFSWatchServer{
		ctx:        ctx,
		requests:   reqs,
		requestsCh: make(chan *WatchRequest, 1),
	}
}

func (m *mockFSWatchServer) Context() context.Context {
	return m.ctx
}

func (m *mockFSWatchServer) Send(resp *WatchResponse) error {
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockFSWatchServer) Recv() (*WatchRequest, error) {
	if len(m.requests) == 0 {
		select {
		case <-m.ctx.Done():
			return nil, m.ctx.Err()
		case req := <-m.requestsCh:
			return req, nil
		}
	}
	req := m.requests[0]
	m.requests = m.requests[1:]
	return req, nil
}

var _ FSWatch_WatchServer = &mockFSWatchServer{}

func TestWatchDir(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dir := t.TempDir()
	mockSrv := newMockServer(ctx, &WatchRequest{
		Request: &WatchRequest_UpdateWatch{
			UpdateWatch: &UpdateWatch{
				Paths: []string{dir + "/"},
			},
		},
	})

	eg := errgroup.Group{}
	eg.Go(func() error {
		watcher := FSWatcherAttachable{}
		err := watcher.Watch(mockSrv)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})

	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/file1.txt", nil, 0644))
	time.Sleep(2 * time.Second)

	cancel()
	require.NoError(t, eg.Wait())

	require.Len(t, mockSrv.responses, 1)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/file1.txt",
			Type: CREATE,
		},
	}, mockSrv.responses[0].GetFileEvents().Events)
}

func TestWatchFile(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/file1.txt", nil, 0644))
	require.NoError(t, os.WriteFile(dir+"/file2.txt", nil, 0644))
	mockSrv := newMockServer(ctx, &WatchRequest{
		Request: &WatchRequest_UpdateWatch{
			UpdateWatch: &UpdateWatch{
				Paths: []string{dir + "/file2.txt"},
			},
		},
	})

	eg := errgroup.Group{}
	eg.Go(func() error {
		watcher := FSWatcherAttachable{}
		err := watcher.Watch(mockSrv)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})

	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/file1.txt", []byte("HELLO"), 0644))
	require.NoError(t, os.WriteFile(dir+"/file2.txt", []byte("HELLO"), 0644))
	time.Sleep(2 * time.Second)

	cancel()
	require.NoError(t, eg.Wait())

	// only file2.txt should trigger an event
	require.GreaterOrEqual(t, len(mockSrv.responses), 1)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/file2.txt",
			Type: WRITE,
		},
	}, mockSrv.responses[0].GetFileEvents().Events)
}

func TestSuccessiveEventsBuffer(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dir := t.TempDir()
	mockSrv := newMockServer(ctx, &WatchRequest{
		Request: &WatchRequest_UpdateWatch{
			UpdateWatch: &UpdateWatch{
				Paths: []string{dir + "/"},
			},
		},
	})

	eg := errgroup.Group{}
	eg.Go(func() error {
		watcher := FSWatcherAttachable{}
		err := watcher.Watch(mockSrv)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})

	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/file1.txt", nil, 0644))
	require.NoError(t, os.WriteFile(dir+"/file2.txt", nil, 0644))
	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/file3.txt", nil, 0644))
	time.Sleep(2 * time.Second)

	cancel()
	require.NoError(t, eg.Wait())

	require.Len(t, mockSrv.responses, 2)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/file1.txt",
			Type: CREATE,
		},
		{
			Path: dir + "/file2.txt",
			Type: CREATE,
		},
	}, mockSrv.responses[0].GetFileEvents().Events)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/file3.txt",
			Type: CREATE,
		},
	}, mockSrv.responses[1].GetFileEvents().Events)
}

func TestUpdateWatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(dir+"/foo", 0755))
	require.NoError(t, os.Mkdir(dir+"/bar", 0755))
	mockSrv := newMockServer(ctx, &WatchRequest{
		Request: &WatchRequest_UpdateWatch{
			UpdateWatch: &UpdateWatch{
				Paths: []string{dir + "/foo/"},
			},
		},
	})

	eg := errgroup.Group{}
	eg.Go(func() error {
		watcher := FSWatcherAttachable{}
		err := watcher.Watch(mockSrv)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	})

	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/foo/file1.txt", nil, 0644))
	require.NoError(t, os.WriteFile(dir+"/bar/file1.txt", nil, 0644))
	time.Sleep(2 * time.Second)

	mockSrv.requestsCh <- &WatchRequest{
		Request: &WatchRequest_UpdateWatch{
			UpdateWatch: &UpdateWatch{
				Paths: []string{dir + "/bar/"},
			},
		},
	}

	time.Sleep(2 * time.Second)
	require.NoError(t, os.WriteFile(dir+"/foo/file2.txt", nil, 0644))
	require.NoError(t, os.WriteFile(dir+"/bar/file2.txt", nil, 0644))
	time.Sleep(2 * time.Second)

	cancel()
	require.NoError(t, eg.Wait())

	require.Len(t, mockSrv.responses, 2)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/foo/file1.txt",
			Type: CREATE,
		},
	}, mockSrv.responses[0].GetFileEvents().Events)
	require.Equal(t, []*FileEvent{
		{
			Path: dir + "/bar/file2.txt",
			Type: CREATE,
		},
	}, mockSrv.responses[1].GetFileEvents().Events)
}
