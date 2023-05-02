package core

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/armon/circbuf"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
)

const (
	// Exec errors will only include the last this number of bytes of output.
	MaxExecErrorOutputBytes = 2 * 1024

	// A magic env var that's interpreted by the shim, telling it to just output
	// the stdout/stderr contents rather than actually execute anything.
	DebugFailedExecEnv = "_DAGGER_SHIM_DEBUG_FAILED_EXEC"
)

// GatewayClient wraps the standard buildkit gateway client with errors that include the output
// of execs when they fail.
type GatewayClient struct {
	bkgw.Client
	refs             map[*ref]struct{}
	cacheConfigType  string
	cacheConfigAttrs map[string]string
	mu               sync.Mutex
}

func NewGatewayClient(baseClient bkgw.Client, cacheConfigType string, cacheConfigAttrs map[string]string) *GatewayClient {
	return &GatewayClient{
		Client:           baseClient,
		cacheConfigType:  cacheConfigType,
		cacheConfigAttrs: cacheConfigAttrs,
		refs:             make(map[*ref]struct{}),
	}
}

func (g *GatewayClient) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *bkgw.Result, rerr error) {
	defer wrapSolveError(&rerr, g.Client)
	if g.cacheConfigType != "" {
		req.CacheImports = []bkgw.CacheOptionsEntry{{
			Type:  g.cacheConfigType,
			Attrs: g.cacheConfigAttrs,
		}}
	}
	res, err := g.Client.Solve(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to solve: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if res.Ref != nil {
		r := &ref{Reference: res.Ref, gw: g}
		g.refs[r] = struct{}{}
		res.Ref = r
	}
	for k, r := range res.Refs {
		r := &ref{Reference: r, gw: g}
		g.refs[r] = struct{}{}
		res.Refs[k] = r
	}
	return res, nil
}

// CombinedResult returns a buildkit result with all the refs solved by this client so far.
// This is useful for constructing a result for remote caching.
func (g *GatewayClient) CombinedResult(ctx context.Context) (*bkgw.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	mergeInputs := make([]llb.State, 0, len(g.refs))
	for r := range g.refs {
		state, err := r.ToState()
		if err != nil {
			return nil, err
		}
		mergeInputs = append(mergeInputs, state)
	}
	llbdef, err := llb.Merge(mergeInputs, llb.WithCustomName("combined session result")).Marshal(ctx)
	if err != nil {
		return nil, err
	}
	mergedRes, err := g.Client.Solve(ctx, bkgw.SolveRequest{
		Definition: llbdef.ToPB(),
	})
	if err != nil {
		return nil, err
	}
	return mergedRes, nil
}

type ref struct {
	bkgw.Reference
	gw *GatewayClient
}

func (r *ref) ReadFile(ctx context.Context, req bkgw.ReadRequest) (_ []byte, rerr error) {
	defer wrapSolveError(&rerr, r.gw.Client)
	return r.Reference.ReadFile(ctx, req)
}

func (r *ref) StatFile(ctx context.Context, req bkgw.StatRequest) (_ *fstypes.Stat, rerr error) {
	defer wrapSolveError(&rerr, r.gw.Client)
	return r.Reference.StatFile(ctx, req)
}

func (r *ref) ReadDir(ctx context.Context, req bkgw.ReadDirRequest) (_ []*fstypes.Stat, rerr error) {
	defer wrapSolveError(&rerr, r.gw.Client)
	return r.Reference.ReadDir(ctx, req)
}

func wrapSolveError(inputErr *error, gw bkgw.Client) {
	if inputErr == nil || *inputErr == nil {
		return
	}
	returnErr := *inputErr

	var se *errdefs.SolveError
	if errors.As(returnErr, &se) {
		// Ensure we don't get blocked trying to return an error by enforcing a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		op := se.Op
		if op == nil || op.Op == nil {
			return
		}
		execOp, ok := se.Op.Op.(*pb.Op_Exec)
		if !ok {
			return
		}

		// This was an exec error, we can retrieve the output still
		// by starting a container w/ the mounts from the failed exec
		// and having our shim output the file contents where the stdout
		// and stderr were stored.
		var mounts []bkgw.Mount
		for i, mnt := range execOp.Exec.Mounts {
			mnt := mnt
			// don't include cache or tmpfs mounts, they shouldn't contain
			// stdout/stderr and we especially don't want to include locked
			// cache mounts as they contend for the cache mount with execs
			// that actually need it.
			if mnt.CacheOpt != nil || mnt.TmpfsOpt != nil {
				continue
			}
			mounts = append(mounts, bkgw.Mount{
				Selector:  mnt.Selector,
				Dest:      mnt.Dest,
				ResultID:  se.MountIDs[i],
				Readonly:  mnt.Readonly,
				MountType: mnt.MountType,
				CacheOpt:  mnt.CacheOpt,
				SecretOpt: mnt.SecretOpt,
				SSHOpt:    mnt.SSHOpt,
			})
		}
		ctr, err := gw.NewContainer(ctx, bkgw.NewContainerRequest{
			Mounts:      mounts,
			NetMode:     execOp.Exec.Network,
			ExtraHosts:  execOp.Exec.Meta.ExtraHosts,
			Platform:    op.Platform,
			Constraints: op.Constraints,
		})
		if err != nil {
			return
		}
		defer func() {
			// Use the background context to release so that it still
			// runs even if there was a timeout or other cancellation.
			// Run in separate go routine on the offchance this unexpectedly
			// blocks a long time (e.g. due to grpc issues).
			go ctr.Release(context.Background())
		}()

		// Use a circular buffer to only save the last N bytes of output, which lets
		// us prevent enormous error messages while retaining the output most likely
		// to be of interest.
		// NOTE: this is technically redundant with the output truncation done by
		// the shim itself now, but still useful as a fallback in case something
		// goes haywire there or if the session is talking to an older engine.
		ctrOut, err := circbuf.NewBuffer(MaxExecErrorOutputBytes)
		if err != nil {
			return
		}
		ctrErr, err := circbuf.NewBuffer(MaxExecErrorOutputBytes)
		if err != nil {
			return
		}
		proc, err := ctr.Start(ctx, bkgw.StartRequest{
			Args: execOp.Exec.Meta.Args,
			// the magic env var is interpreted by the shim, telling it to just output
			// the stdout/stderr contents rather than actually execute anything.
			Env:    append(execOp.Exec.Meta.Env, DebugFailedExecEnv+"=1"),
			User:   execOp.Exec.Meta.User,
			Cwd:    execOp.Exec.Meta.Cwd,
			Stdout: &nopCloser{ctrOut},
			Stderr: &nopCloser{ctrErr},
		})
		if err != nil {
			return
		}
		if err := proc.Wait(); err != nil {
			return
		}
		stdout := strings.TrimSpace(ctrOut.String())
		stderr := strings.TrimSpace(ctrErr.String())
		returnErr = fmt.Errorf("%w\nStdout:\n%s\nStderr:\n%s", returnErr, stdout, stderr)
	}
	*inputErr = returnErr
}

type nopCloser struct {
	io.Writer
}

func (n *nopCloser) Close() error {
	return nil
}
