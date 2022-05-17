package solver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/frontend/gateway/client"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress"
	"github.com/moby/buildkit/util/progress/logs"
	"github.com/opencontainers/go-digest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type container struct {
	id   string // unique random id
	name string // human-readable (possibly not unique) name

	ctr  client.Container
	proc client.ContainerProcess

	mu       sync.Mutex
	stopped  bool
	exitCode uint8
	exitErr  error

	progressWriter progress.Writer
	cancelProgress func()
	started        *time.Time
}

type StartContainerRequest struct {
	Name      string
	Container client.NewContainerRequest
	Proc      client.StartRequest
}

func (s *Solver) StartContainer(ctx context.Context, req StartContainerRequest) (string, error) {
	ctr, err := s.opts.Gateway.NewContainer(ctx, req.Container)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	id := digest.FromString(identity.NewID())
	if req.Name == "" {
		req.Name = string(id)
	}

	progressWriter, stdout, stderr, cancelProgress := s.forwardProgress(id, log.Ctx(ctx))
	req.Proc.Stdout = stdout
	req.Proc.Stderr = stderr

	lg := log.Ctx(ctx)
	started := time.Now()
	if err := progressWriter.Write(identity.NewID(), bk.Vertex{
		Digest:        id,
		Started:       &started,
		ProgressGroup: &pb.ProgressGroup{Id: req.Name, Name: req.Name},
	}); err != nil {
		lg.Error().Err(err).Msg("failed to write progress")
	}

	proc, err := ctr.Start(ctx, req.Proc)
	if err != nil {
		cancelProgress()
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	s.containersMu.Lock()
	s.containers[id.String()] = &container{
		id:             id.String(),
		name:           req.Name,
		ctr:            ctr,
		proc:           proc,
		progressWriter: progressWriter,
		cancelProgress: cancelProgress,
		started:        &started,
	}
	s.containersMu.Unlock()
	return id.String(), nil
}

func (s *Solver) ContainerName(ctrID string) (string, error) {
	s.containersMu.RLock()
	defer s.containersMu.RUnlock()
	c, ok := s.containers[ctrID]
	if !ok {
		return "", ContainerNotFoundError{ID: ctrID}
	}
	return c.name, nil
}

func (s *Solver) SignalContainer(ctx context.Context, ctrID string, sig syscall.Signal) error {
	s.containersMu.Lock()
	c, ok := s.containers[ctrID]
	s.containersMu.Unlock()
	if !ok {
		return ContainerNotFoundError{ID: ctrID}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return ContainerAlreadyStoppedError{ID: ctrID}
	}

	return c.proc.Signal(ctx, sig)
}

func (s *Solver) StopContainer(ctx context.Context, ctrID string, timeout time.Duration) (uint8, error) {
	s.containersMu.Lock()
	c, ok := s.containers[ctrID]
	s.containersMu.Unlock()
	if !ok {
		return 0, ContainerNotFoundError{ID: ctrID}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return c.exitCode, c.exitErr
	}
	c.stopped = true

	lg := log.Ctx(ctx)
	defer func() {
		stopped := time.Now()
		if err := c.progressWriter.Write(identity.NewID(), bk.Vertex{
			Digest:        digest.Digest(c.id),
			Started:       c.started,
			Completed:     &stopped,
			ProgressGroup: &pb.ProgressGroup{Id: c.name, Name: c.name},
		}); err != nil {
			lg.Error().Err(err).Msg("failed to write progress")
		}
		c.cancelProgress()
	}()

	// FIXME: buildkit currently leaks containers if client crashes.
	// https://github.com/moby/buildkit/issues/2811
	// This needs to be fixed upstream, but for now if this happens
	// the only remidiation for users is to let the container exit
	// on its own (if possible) or to restart buildkitd.

	if timeout > 0 {
		waitCh := make(chan struct{})
		go func() {
			defer close(waitCh)
			c.proc.Wait()
		}()
		select {
		case <-waitCh:
		case <-time.After(timeout):
		}
	}

	// Releasing the container sends SIGKILL to the process if not already dead.
	c.exitCode, c.exitErr = getExitCode(c.ctr.Release(ctx))
	return c.exitCode, c.exitErr
}

func getExitCode(err error) (uint8, error) {
	if err == nil {
		return 0, nil
	}
	exitError := &gatewayapi.ExitError{}
	if errors.As(err, &exitError) {
		// if the only thing that went wrong was the container exiting non-zero,
		// just return the exit code and no error
		if exitError.ExitCode != gatewayapi.UnknownExitStatus {
			return uint8(exitError.ExitCode), nil
		}
	}
	return 0, err
}

func (s *Solver) forwardProgress(id digest.Digest, lg *zerolog.Logger) (progress.Writer, io.WriteCloser, io.WriteCloser, func()) {
	reader, readerCtx, stopReader := progress.NewContext(context.Background())
	// for some reason, stopReader is a cancel associated with a different context,
	// so we have to use both that and a separate readerCancel created here
	readerCtx, readerCancel := context.WithCancel(readerCtx)

	writer, _, writerCtx := progress.NewFromContext(readerCtx, progress.WithMetadata("vertex", id))

	stdout, stderr, flushLogs := logs.NewLogStreams(writerCtx, false)

	cancelAll := func() {
		writer.Close()
		flushLogs()
		stopReader()
		readerCancel()
	}

	s.eventsWg.Add(1)
	go func() {
		defer s.eventsWg.Done()

		for {
			pgresses, err := reader.Read(readerCtx)
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
					lg.Error().Err(err).Msg("failed to read progress")
				}
				return
			}
			statuses := &bk.SolveStatus{}
			for _, pgress := range pgresses {
				switch v := pgress.Sys.(type) {
				case bk.Vertex:
					statuses.Vertexes = append(statuses.Vertexes, &v)
				case bk.VertexLog:
					vtx, ok := pgress.Meta("vertex")
					if !ok {
						lg.Debug().Msg("failed to find vertex in progress")
						continue
					}
					v.Vertex = vtx.(digest.Digest)
					v.Timestamp = pgress.Timestamp
					statuses.Logs = append(statuses.Logs, &v)
				}
			}
			select {
			case <-readerCtx.Done():
				return
			case s.opts.Events <- statuses:
			}
		}
	}()

	return writer, stdout, stderr, cancelAll
}

type ContainerNotFoundError struct {
	ID string
}

func (e ContainerNotFoundError) Error() string {
	return fmt.Sprintf("container %s not found", e.ID)
}

type ContainerAlreadyStoppedError struct {
	ID string
}

func (e ContainerAlreadyStoppedError) Error() string {
	return fmt.Sprintf("container %s already stopped", e.ID)
}
