package core

import (
	"context"
	"sync"

	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
)

type Session struct {
	bkClient  *bkclient.Client
	solveOpts bkclient.SolveOpt
	solveCh   chan<- *bkclient.SolveStatus
	mirrorChs *sync.WaitGroup
}

func NewSession(bkClient *bkclient.Client, solveOpts bkclient.SolveOpt, solveCh chan<- *bkclient.SolveStatus) *Session {
	return &Session{
		bkClient:  bkClient,
		solveOpts: solveOpts,
		solveCh:   solveCh,
		mirrorChs: new(sync.WaitGroup),
	}
}

func (s *Session) WithLocalDirs(localDirs map[string]string) *Session {
	cp := *s
	cpDirs := map[string]string{}
	for id, dir := range s.solveOpts.LocalDirs {
		cpDirs[id] = dir
	}
	for id, dir := range localDirs {
		cpDirs[id] = dir
	}
	cp.solveOpts.LocalDirs = cpDirs
	return &cp
}

func (s *Session) WithExport(export bkclient.ExportEntry) *Session {
	cp := *s
	cpExports := []bkclient.ExportEntry{}
	copy(cpExports, s.solveOpts.Exports)
	cpExports = append(cpExports, export)
	cp.solveOpts.Exports = cpExports
	return &cp
}

func (s *Session) Build(ctx context.Context, f bkgw.BuildFunc) error {
	s.mirrorChs.Add(1)
	mirrorCh, wg := mirrorCh(s.solveCh)
	defer func() {
		wg.Wait()
		s.mirrorChs.Done()
	}()
	solveOpts := s.solveOpts
	// XXX(vito): explain this trickery
	solveOpts.Session = append([]session.Attachable{secretsprovider.NewSecretProvider(s)}, solveOpts.Session...)
	_, err := s.bkClient.Build(ctx, solveOpts, "", f, mirrorCh)
	return err
}

func (s *Session) Wait() {
	s.mirrorChs.Wait()
}

var _ secrets.SecretStore = &Session{}

func (s *Session) GetSecret(ctx context.Context, id string) ([]byte, error) {
	return NewSecret(SecretID(id)).Plaintext(ctx, s)
}

// mirrorCh mirrors messages from one channel to another, protecting the
// destination channel from being closed.
//
// this is used to reflect Build/Solve progress in a longer-lived progress UI,
// since they close the channel when they're done.
func mirrorCh[T any](dest chan<- T) (chan T, *sync.WaitGroup) {
	wg := new(sync.WaitGroup)

	if dest == nil {
		return nil, wg
	}

	mirrorCh := make(chan T)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range mirrorCh {
			dest <- event
		}
	}()

	return mirrorCh, wg
}
