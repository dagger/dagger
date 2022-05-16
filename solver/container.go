package solver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/moby/buildkit/frontend/gateway/client"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/identity"
)

type container struct {
	id   string
	ctr  client.Container
	proc client.ContainerProcess

	mu       sync.Mutex
	stopped  bool
	exitCode uint8
	exitErr  error
}

type StartContainerRequest struct {
	Container client.NewContainerRequest
	Proc      client.StartRequest
}

func (s *Solver) StartContainer(ctx context.Context, req StartContainerRequest) (string, error) {
	ctr, err := s.opts.Gateway.NewContainer(ctx, req.Container)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	proc, err := ctr.Start(ctx, req.Proc)
	if err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	id := identity.NewID()
	s.containersMu.Lock()
	s.containers[id] = &container{
		id:   id,
		ctr:  ctr,
		proc: proc,
	}
	s.containersMu.Unlock()
	return id, nil
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
