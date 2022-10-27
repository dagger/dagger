package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type Host struct {
	Workdir   string
	DisableRW bool
}

func NewHost(workdir string, disableRW bool) *Host {
	return &Host{
		Workdir:   workdir,
		DisableRW: disableRW,
	}
}

type HostVariable struct {
	Name string `json:"name"`
}

func (host *Host) Directory(ctx context.Context, dirPath string, platform specs.Platform) (*Directory, error) {
	if host.DisableRW {
		return nil, ErrHostRWDisabled
	}

	var absPath string
	var err error
	if filepath.IsAbs(dirPath) {
		absPath = dirPath
	} else {
		absPath = filepath.Join(host.Workdir, dirPath)

		if !strings.HasPrefix(absPath, host.Workdir) {
			return nil, fmt.Errorf("path %q escapes workdir; use an absolute path instead", dirPath)
		}
	}

	absPath, err = filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, fmt.Errorf("eval symlinks: %w", err)
	}

	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(llb.Copy(llb.Local(
		absPath, // TODO: is there a better thing to set here? ...seems ok?
		llb.SharedKeyHint(absPath),
		llb.LocalUniqueID(absPath),
		// FIXME: should not be hardcoded
		llb.ExcludePatterns([]string{"**/node_modules"}),
	), "/", "/"))

	return NewDirectory(ctx, st, "", platform)
}

func (host *Host) Export(
	ctx context.Context,
	dest string,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
	buildFn bkgw.BuildFunc,
) error {
	if host.DisableRW {
		return ErrHostRWDisabled
	}

	solveOpts.Exports = []bkclient.ExportEntry{
		{
			Type:      bkclient.ExporterLocal,
			OutputDir: dest,
		},
	}

	ch, wg := mirrorCh(solveCh)
	defer wg.Wait()

	_, err := bkClient.Build(ctx, solveOpts, "", buildFn, ch)
	return err
}
