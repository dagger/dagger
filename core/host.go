package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type HostVariable struct {
	Name string `json:"name"`
}

type HostDirectory struct {
	ID HostDirectoryID `json:"id"`
}

func (dir *HostDirectory) Read(ctx context.Context, platform specs.Platform) (*Directory, error) {
	id := string(dir.ID)

	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(llb.Copy(llb.Local(
		id,
		// TODO: better shared key hint?
		llb.SharedKeyHint(id),
		// FIXME: should not be hardcoded
		llb.ExcludePatterns([]string{"**/node_modules"}),
	), "/", "/"))

	return NewDirectory(ctx, st, "", platform)
}

func (dir *HostDirectory) Write(
	ctx context.Context,
	localDir, dest string,
	source *Directory,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) (bool, error) {
	dest, err := filepath.Abs(filepath.Join(localDir, dest))
	if err != nil {
		return false, err
	}

	// Ensure the destination is a sub-directory of the workdir
	dest, err = filepath.EvalSymlinks(dest)
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(dest, localDir) {
		return false, fmt.Errorf("path %q is outside workdir", dest)
	}

	solveOpts.Exports = []bkclient.ExportEntry{
		{
			Type:      bkclient.ExporterLocal,
			OutputDir: dest,
		},
	}

	// Mirror events from the sub-Build into the main Build event channel.
	// Build() will close the channel after completion so we don't want to use the main channel directly.
	ch := make(chan *bkclient.SolveStatus)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range ch {
			solveCh <- event
		}
	}()

	_, err = bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		src, rel, platform, err := source.Decode()
		if err != nil {
			return nil, err
		}

		if rel != "" {
			src = llb.Scratch().File(llb.Copy(src, rel, ".", &llb.CopyInfo{
				CopyDirContentsOnly: true,
			}))
		}

		def, err := src.Marshal(ctx, llb.Platform(platform))
		if err != nil {
			return nil, err
		}

		return gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
	}, ch)
	if err != nil {
		return false, err
	}

	wg.Wait()

	return true, nil
}

type HostDirectoryID string
