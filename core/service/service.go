package service

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/google/uuid"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	solverapi "github.com/moby/buildkit/solver/pb"
)

type Config struct {
	Mounts  map[string]*filesystem.Filesystem
	Args    []string
	Env     []string
	Workdir string
}

func New(gw bkgw.Client, cfg *Config) *Service {
	id := strings.ReplaceAll(uuid.New().String(), "-", "")

	svc := &Service{
		id:       id,
		cfg:      cfg,
		gw:       gw,
		exitCode: 1,
		doneCh:   make(chan struct{}),
	}

	return svc
}

type Service struct {
	id  string
	cfg *Config
	gw  bkgw.Client

	ctr  bkgw.Container
	proc bkgw.ContainerProcess

	doneCh   chan struct{}
	exitCode int

	stdin  io.ReadCloser
	stdout io.WriteCloser
	stderr io.WriteCloser
}

func (s *Service) ID() string {
	return s.id
}

func (s *Service) Config() *Config {
	return s.cfg
}

func (s *Service) Done() chan struct{} {
	return s.doneCh
}

func (s *Service) ExitCode() int {
	<-s.doneCh
	return s.exitCode
}

func (s *Service) StdinPipe() (io.Writer, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	s.stdin = stdinR
	return stdinW, nil
}

func (s *Service) StdoutPipe() (io.Reader, error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	s.stdout = stdoutW
	return stdoutR, nil
}

func (s *Service) StderrPipe() (io.Reader, error) {
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	s.stderr = stderrW
	return stderrR, nil
}

func (s *Service) Resize(ctx context.Context, cols, rows int) error {
	return s.proc.Resize(ctx, bkgw.WinSize{
		Cols: uint32(cols),
		Rows: uint32(rows),
	})
}

func (s *Service) Start(ctx context.Context) error {
	mounts, err := convertMounts(ctx, s.gw, s.cfg.Mounts)
	if err != nil {
		return err
	}

	s.ctr, err = s.gw.NewContainer(ctx, bkgw.NewContainerRequest{
		Mounts: mounts,
	})
	if err != nil {
		return err
	}

	s.proc, err = s.ctr.Start(ctx, bkgw.StartRequest{
		Env:    s.cfg.Env,
		Args:   s.cfg.Args,
		Cwd:    s.cfg.Workdir,
		Tty:    true,
		Stdin:  s.stdin,
		Stdout: s.stdout,
		Stderr: s.stderr,
	})
	go func() {
		defer close(s.doneCh)
		err := s.proc.Wait()

		var exitError *gatewayapi.ExitError
		if errors.As(err, &exitError) {
			s.exitCode = int(exitError.ExitCode)
		}

		s.ctr.Release(ctx)
	}()
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) Stop(ctx context.Context, wait time.Duration) error {
	// Attempt to graciously terminate the service
	if err := s.proc.Signal(ctx, syscall.SIGTERM); err != nil {
		return err
	}

	// If not terminated within 10 seconds, kill it
	select {
	case <-s.doneCh:
		return nil
	case <-time.After(wait):
		if err := s.proc.Signal(ctx, syscall.SIGKILL); err != nil {
			return err
		}
		<-s.doneCh
		return nil
	}
}

func convertMounts(ctx context.Context, gw bkgw.Client, mounts map[string]*filesystem.Filesystem) ([]bkgw.Mount, error) {
	mountList := make([]bkgw.Mount, 0, len(mounts))

	for path, fs := range mounts {
		def, err := fs.ToDefinition()
		if err != nil {
			return nil, err
		}

		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Definition: def,
		})
		if err != nil {
			return nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		mountList = append(mountList, bkgw.Mount{
			Dest:      path,
			MountType: solverapi.MountType_BIND,
			Ref:       ref,
		})
	}

	return mountList, nil
}
