package solver

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

func (s *Solver) StopContainer(ctx context.Context, ctrID string) (uint8, error) {
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

	// Releasing the container sends SIGKILL to the process.
	// Support for sending other signals first is not implemented yet, but can be.
	exitCode, err := getExitCode(c.ctr.Release(ctx))
	if err != nil {
		c.exitErr = fmt.Errorf("failed to release container: %w", err)
		return 0, c.exitErr
	}
	c.exitCode = exitCode

	// Wait for the process to exit. The call doesn't accept a context, so make
	// it cancellable with a separate goroutine.
	waitCh := make(chan error)
	go func() {
		defer close(waitCh)
		// we don't need the exit code here, but we want to ignore errors due to it being set
		if _, err := getExitCode(c.proc.Wait()); err != nil {
			waitCh <- fmt.Errorf("failed to wait for container process: %w", err)
		}
	}()
	select {
	case <-ctx.Done():
		c.exitErr = ctx.Err()
		return c.exitCode, c.exitErr
	case err := <-waitCh:
		c.exitErr = err
		return c.exitCode, c.exitErr
	}
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
