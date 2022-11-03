package core

// import (
// 	"context"
// 	"sync"

// 	bkclient "github.com/moby/buildkit/client"
// 	bkgw "github.com/moby/buildkit/frontend/gateway/client"
// 	"github.com/moby/buildkit/session"
// 	"github.com/moby/buildkit/session/secrets/secretsprovider"
// )

// type Session struct {
// 	bkClient  *bkclient.Client
// 	solveOpts bkclient.SolveOpt
// 	solveCh   chan<- *bkclient.SolveStatus
// 	mirrorChs *sync.WaitGroup
// }

// func NewSession(bkClient *bkclient.Client, solveOpts bkclient.SolveOpt, solveCh chan<- *bkclient.SolveStatus) *Session {
// 	s := &Session{
// 		bkClient:  bkClient,
// 		solveOpts: solveOpts,
// 		solveCh:   solveCh,
// 		mirrorChs: new(sync.WaitGroup),
// 	}

// 	s.solveOpts.Session = append(
// 		[]session.Attachable{
// 			// NB: use the session itself as the secret store. it accepts SecretIDs,
// 			// which might reference an LLB definition to run.
// 			secretsprovider.NewSecretProvider(s),
// 		},
// 		solveOpts.Session...,
// 	)

// 	return s
// }

// func (s *Session) WithClientSession(caller session.Caller) *Session {
// 	return s
// }

// func (s *Session) WithLocalDirs(localDirs []string) *Session {
// 	cp := *s
// 	cpDirs := map[string]string{}
// 	for id, dir := range s.solveOpts.LocalDirs {
// 		cpDirs[id] = dir
// 	}
// 	for _, dir := range localDirs {
// 		cpDirs[dir] = dir
// 	}
// 	cp.solveOpts.LocalDirs = cpDirs
// 	return &cp
// }

// func (s *Session) WithExport(export bkclient.ExportEntry) *Session {
// 	cp := *s
// 	cpExports := []bkclient.ExportEntry{}
// 	copy(cpExports, s.solveOpts.Exports)
// 	cpExports = append(cpExports, export)
// 	cp.solveOpts.Exports = cpExports
// 	return &cp
// }

// func (s *Session) Build(ctx context.Context, f bkgw.BuildFunc) (*bkclient.SolveResponse, error) {
// 	s.mirrorChs.Add(1)
// 	mirrorCh, wg := mirrorCh(s.solveCh)
// 	defer func() {
// 		wg.Wait()
// 		s.mirrorChs.Done()
// 	}()
// 	return s.bkClient.Build(ctx, s.solveOpts, "", f, mirrorCh)
// }

// func (s *Session) Wait() {
// 	s.mirrorChs.Wait()
// }

// // var _ secrets.SecretStore = &Session{}

// // func (s *Session) GetSecret(ctx context.Context, id string) ([]byte, error) {
// // 	return NewSecret(SecretID(id)).Plaintext(ctx, s)
// // }
