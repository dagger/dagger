package cacerts

import (
	"context"
	"errors"
	"os"

	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/engine/buildkit/containerfs"
)

const (
	EngineCustomCACertsDir = "/usr/local/share/ca-certificates"
)

// Installer is an implementation of installing+uninstalling custom CA certs for a container,
// usually based on the distro.
type Installer interface {
	// Install installs the custom CA certs into the container. In case of an error part way through,
	// it should attempt to cleanup after itself and return the error. If cleanup itself errors, it should
	// be returned wrapped in a CleanupErr type.
	Install(ctx context.Context) error
	// Uninstall removes the custom CA certs from the container.
	Uninstall(context.Context) error
	// detect checks if the container is a match for this installer.
	detect() (bool, error)
	// initialize sets the installer's initial internal state
	initialize(*containerfs.ContainerFS) error
}

func NewInstaller(
	ctx context.Context,
	spec *specs.Spec,
	executeContainer containerfs.ExecuteContainerFunc,
) (Installer, error) {
	dirEnts, err := os.ReadDir(EngineCustomCACertsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(dirEnts) == 0 {
		return noopInstaller{}, nil
	}

	ctrFS, err := containerfs.NewContainerFS(spec, executeContainer)
	if err != nil {
		return nil, err
	}

	// Run detection in parallel but unblock as soon as one match is found
	// in which case it will be used. This way, we only block on every detect
	// finishing in the case where no match is found or any error happens.
	var eg errgroup.Group
	matchChan := make(chan Installer, 1)
	for _, installer := range []Installer{
		&debianLike{},
		&rhelLike{},
	} {
		installer := installer
		eg.Go(func() error {
			if err := installer.initialize(ctrFS); err != nil {
				return err
			}
			match, err := installer.detect()
			if err != nil {
				return err
			}
			if match {
				select {
				case matchChan <- installer:
				default:
				}
			}
			return nil
		})
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- eg.Wait()
	}()

	select {
	case match := <-matchChan:
		return match, nil
	case err := <-errChan:
		// double check there wasn't an obscure race condition where a match
		// was found but we weren't signaled until after the errgroup finished
		select {
		case match := <-matchChan:
			return match, nil
		default:
		}
		if err != nil {
			return nil, err
		}
	}

	// no match found
	return noopInstaller{}, nil
}

type noopInstaller struct{}

func (noopInstaller) Install(context.Context) error             { return nil }
func (noopInstaller) Uninstall(context.Context) error           { return nil }
func (noopInstaller) detect() (bool, error)                     { return false, nil }
func (noopInstaller) initialize(*containerfs.ContainerFS) error { return nil }

// Want identifiable separate type for cleanup errors since if those are
// hit specifically we need to fail to the whole exec (whereas other errors
// but successful cleanup can be non-fatal)
type CleanupErr struct {
	err error
}

func (c CleanupErr) Error() string {
	return c.err.Error()
}

func (c CleanupErr) Unwrap() error {
	return c.err
}

type cleanups struct {
	funcs []func() error
}

func (c *cleanups) append(f func() error) {
	c.funcs = append(c.funcs, f)
}

func (c *cleanups) prepend(f func() error) {
	c.funcs = append([]func() error{f}, c.funcs...)
}

func (c *cleanups) run() error {
	var rerr error
	for i := len(c.funcs) - 1; i >= 0; i-- {
		if err := c.funcs[i](); err != nil {
			rerr = errors.Join(rerr, CleanupErr{err})
		}
	}
	return rerr
}
