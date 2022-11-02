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

type CopyFilter struct {
	Exclude []string
	Include []string
}

func (host *Host) Directory(ctx context.Context, dirPath string, platform specs.Platform, filter CopyFilter) (*Directory, error) {
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

	localID := fmt.Sprintf("host:%s", absPath)

	localOpts := []llb.LocalOption{
		// synchronize concurrent filesyncs for the same path
		llb.SharedKeyHint(localID),

		// make the LLB stable so we can test invariants like:
		//
		//   workdir == directory(".")
		llb.LocalUniqueID(localID),
	}

	if len(filter.Exclude) > 0 {
		localOpts = append(localOpts, llb.ExcludePatterns(filter.Exclude))
	}

	if len(filter.Include) > 0 {
		localOpts = append(localOpts, llb.IncludePatterns(filter.Include))
	}

	// copy to scratch to avoid making buildkit's snapshot of the local dir immutable,
	// which makes it unable to reused, which in turn creates cache invalidations
	// TODO: this should be optional, the above issue can also be avoided w/ readonly
	// mount when possible
	st := llb.Scratch().File(llb.Copy(llb.Local(absPath, localOpts...), "/", "/"))

	return NewDirectory(ctx, st, "", platform)
}

func (host *Host) Export(
	ctx context.Context,
	export bkclient.ExportEntry,
	dest string,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
	buildFn bkgw.BuildFunc,
) error {
	if host.DisableRW {
		return ErrHostRWDisabled
	}

	ch, wg := mirrorCh(solveCh)
	defer wg.Wait()

	solveOpts.Exports = []bkclient.ExportEntry{export}

	_, err := bkClient.Build(ctx, solveOpts, "", buildFn, ch)
	return err
}

func (host *Host) NormalizeDest(dest string) (string, error) {
	if filepath.IsAbs(dest) {
		return dest, nil
	}

	wd, err := filepath.EvalSymlinks(host.Workdir)
	if err != nil {
		return "", err
	}

	dest = filepath.Clean(filepath.Join(wd, dest))

	if dest == wd {
		// writing directly to workdir
		return dest, nil
	}

	if !strings.HasPrefix(dest, wd+"/") {
		// writing outside of workdir
		return "", fmt.Errorf("destination %q escapes workdir", dest)
	}

	// writing within workdir
	return dest, nil
}
