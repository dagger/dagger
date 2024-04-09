package cacerts

import (
	"context"
	"errors"
	"os"

	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sync/errgroup"
)

const (
	EngineCustomCACertsDir = "/usr/local/share/ca-certificates"
)

type Installer interface {
	Install(ctx context.Context) error
	Uninstall(context.Context) error
	detect(context.Context) (bool, error)
}

type executeContainerFunc func(ctx context.Context, args ...string) error

func NewInstaller(
	ctx context.Context,
	spec *specs.Spec,
	executeContainer executeContainerFunc,
) (Installer, error) {
	dirEnts, err := os.ReadDir(EngineCustomCACertsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(dirEnts) == 0 {
		return noopInstaller{}, nil
	}

	ctrFS, err := newContainerFS(spec, executeContainer)
	if err != nil {
		return nil, err
	}

	var eg errgroup.Group
	matchChan := make(chan Installer, 1)
	for _, installer := range []Installer{
		newDebianLike(ctrFS),
		newRhelLike(ctrFS),
	} {
		installer := installer
		eg.Go(func() error {
			match, err := installer.detect(ctx)
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

func (noopInstaller) Install(context.Context) error        { return nil }
func (noopInstaller) Uninstall(context.Context) error      { return nil }
func (noopInstaller) detect(context.Context) (bool, error) { return false, nil }
