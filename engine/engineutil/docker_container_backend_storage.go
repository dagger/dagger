package engineutil

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	containertypes "github.com/docker/docker/api/types/container"
)

type dockerContainerStatus string

const (
	dockerContainerStatusCreated dockerContainerStatus = "created"
	dockerContainerStatusRunning dockerContainerStatus = "running"
	dockerContainerStatusExited  dockerContainerStatus = "exited"
)

// dockerContainerStream is a concurrent-safe append-only byte buffer.
// Writers call Write; readers call ReadFrom(offset) to get bytes from a
// given position, blocking until new data arrives or the stream is closed.
type dockerContainerStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newDockerContainerStream() *dockerContainerStream {
	s := &dockerContainerStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Write implements [io.Writer]. Safe for concurrent use.
func (s *dockerContainerStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	s.buf = append(s.buf, p...)
	s.cond.Broadcast()
	return len(p), nil
}

// Close marks the stream as done and unblocks all readers.
func (s *dockerContainerStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

// ReadFrom blocks until at least one byte is available past offset, then
// returns all currently buffered bytes from that offset onward.
// Returns (data, newOffset, io.EOF) when the stream is closed and fully consumed.
func (s *dockerContainerStream) ReadFrom(ctx context.Context, offset int) ([]byte, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) <= offset && !s.closed {
		// Release lock and wait, but respect context cancellation.
		waitDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				s.cond.Broadcast()
			case <-waitDone:
			}
		}()
		s.cond.Wait()
		close(waitDone)
		if ctx.Err() != nil {
			return nil, offset, ctx.Err()
		}
	}
	data := s.buf[offset:]
	if len(data) == 0 && s.closed {
		return nil, offset, io.EOF
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, offset + len(data), nil
}

// Bytes returns a snapshot of all buffered bytes. Safe after Close.
func (s *dockerContainerStream) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.buf))
	copy(out, s.buf)
	return out
}

type dockerContainer struct {
	mu sync.RWMutex

	id       string
	name     string
	imageRef string
	config   *containertypes.Config
	status   dockerContainerStatus
	exitCode int

	// cancel stops the running container
	cancel context.CancelCauseFunc
	// done is closed when the container has fully stopped
	done chan struct{}
	// waitErr is the error returned by c.Run when done is closed
	waitErr error

	// stdin pipe: client writes, container reads
	stdinR *io.PipeReader
	stdinW *io.PipeWriter

	// stdout/stderr accumulate output and support live readers
	stdout *dockerContainerStream
	stderr *dockerContainerStream

	// portBindings from HostConfig.PortBindings (-p flags)
	hostConfig *containertypes.HostConfig
}

type dockerContainerBackendStorage interface {
	Get(ctx context.Context, nameOrIDPrefix string) (dc *dockerContainer, found bool, err error)
	Create(ctx context.Context, dc *dockerContainer) (err error)
	Delete(ctx context.Context, id string) (err error)
	List(ctx context.Context) ([]*dockerContainer, error)
}

type dockerContainerBackendStorageSyncMap struct {
	sync.Map
}

var _ dockerContainerBackendStorage = (*dockerContainerBackendStorageSyncMap)(nil)

// Get implements [dockerContainerBackendStorage].
// nameOrID may be a full container ID, a short ID prefix, or a name.
// Returns an error if multiple containers match the same short ID prefix.
func (d *dockerContainerBackendStorageSyncMap) Get(_ context.Context, nameOrIDPrefix string) (*dockerContainer, bool, error) {
	var exact *dockerContainer
	var prefixMatches []*dockerContainer

	d.Map.Range(func(_, v any) bool {
		dc := v.(*dockerContainer)
		if dc.id == nameOrIDPrefix || dc.name == nameOrIDPrefix {
			exact = dc
			return false
		}
		if strings.HasPrefix(dc.id, nameOrIDPrefix) {
			prefixMatches = append(prefixMatches, dc)
		}
		return true
	})

	if exact != nil {
		return exact, true, nil
	}
	switch len(prefixMatches) {
	case 0:
		return nil, false, nil
	case 1:
		return prefixMatches[0], true, nil
	default:
		return nil, false, fmt.Errorf("container ID %q is ambiguous (%d matches)", nameOrIDPrefix, len(prefixMatches))
	}
}

// Create implements [dockerContainerBackendStorage].
// Keyed by container ID.
func (d *dockerContainerBackendStorageSyncMap) Create(_ context.Context, dc *dockerContainer) error {
	d.Map.Store(dc.id, dc)
	return nil
}

// Delete implements [dockerContainerBackendStorage].
func (d *dockerContainerBackendStorageSyncMap) Delete(_ context.Context, id string) error {
	d.Map.Delete(id)
	return nil
}

// List implements [dockerContainerBackendStorage].
func (d *dockerContainerBackendStorageSyncMap) List(_ context.Context) ([]*dockerContainer, error) {
	var out []*dockerContainer
	d.Map.Range(func(_, v any) bool {
		out = append(out, v.(*dockerContainer))
		return true
	})
	return out, nil
}
