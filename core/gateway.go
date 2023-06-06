package core

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/armon/circbuf"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vito/progrock"
)

const (
	// Exec errors will only include the last this number of bytes of output.
	MaxExecErrorOutputBytes = 30 * 1024

	// TruncationMessage is the message that will be prepended to truncated output.
	TruncationMessage = "[omitting %d bytes]..."

	// MaxFileContentsChunkSize sets the maximum chunk size for ReadFile calls
	// Equals around 95% of the max message size (16777216) in
	// order to keep space for any Protocol Buffers overhead:
	MaxFileContentsChunkSize = 15938355

	// MaxFileContentsSize sets the limit of the maximum file size
	// that can be retrieved using File.Contents, currently set to 128MB:
	MaxFileContentsSize = 128 << 20

	// A magic env var that's interpreted by the shim, telling it to just output
	// the stdout/stderr contents rather than actually execute anything.
	DebugFailedExecEnv = "_DAGGER_SHIM_DEBUG_FAILED_EXEC"
)

// GatewayClient wraps the standard buildkit gateway client with a few extensions:
//
// * Errors include the output of execs when they fail.
// * Vertexes are joined to the Progrock group using the recorder from ctx.
// * Cache imports can be configured across all Solves.
// * All Solved results can be retrieved for cache exports.
type GatewayClient struct {
	bkgw.Client
	refs             map[*ref]struct{}
	cacheConfigType  string
	cacheConfigAttrs map[string]string
	mu               sync.Mutex
}

func NewGatewayClient(baseClient bkgw.Client, cacheConfigType string, cacheConfigAttrs map[string]string) *GatewayClient {
	return &GatewayClient{
		// Wrap the client with recordingGateway just so we can separate concerns a
		// tiny bit.
		Client: recordingGateway{baseClient},

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

		maxTruncMsg := fmt.Sprintf(TruncationMessage, int64(math.MaxInt64))
		maxOutputBytes := int64(MaxExecErrorOutputBytes + len(maxTruncMsg))

		// Use a circular buffer to only save the last N bytes of output, which lets
		// us prevent enormous error messages while retaining the output most likely
		// to be of interest.
		// NOTE: this is technically redundant with the output truncation done by
		// the shim itself now, but still useful as a fallback in case something
		// goes haywire there or if the session is talking to an older engine.
		ctrOut, err := circbuf.NewBuffer(maxOutputBytes)
		if err != nil {
			return
		}
		ctrErr, err := circbuf.NewBuffer(maxOutputBytes)
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

		err = proc.Wait()

		exitCode := -1 // -1 indicates failure to get exit code
		if err != nil {
			var exitErr *bkpb.ExitError
			if errors.As(err, &exitErr) {
				exitCode = int(exitErr.ExitCode)
			} else {
				// This can happen for example if debugging the failed exec
				// takes longer than the timeout in this context, but since
				// we know the exec op failed, try to return what we have
				// at this point with the ExecError. The exit code will be -1
				// and stdout/stderr output may not be complete.
				returnErr = fmt.Errorf("[%w]: %w", err, returnErr)
			}
		}

		returnErr = &ExecError{
			original: returnErr,
			Cmd:      execOp.Exec.Meta.Args,
			ExitCode: exitCode,
			Stdout:   strings.TrimSpace(ctrOut.String()),
			Stderr:   strings.TrimSpace(ctrErr.String()),
		}
	}
	*inputErr = returnErr
}

type nopCloser struct {
	io.Writer
}

func (n nopCloser) Close() error {
	return nil
}

type recordingGateway struct {
	bkgw.Client
}

// ResolveImageConfig records the image config resolution vertex as a member of
// the current progress group, and calls the inner ResolveImageConfig.
func (g recordingGateway) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
	rec := progrock.RecorderFromContext(ctx)

	// HACK(vito): this is how Buildkit determines the vertex digest. Keep this
	// in sync with Buildkit until a better way to do this arrives. It hasn't
	// changed in 5 years, surely it won't soon, right?
	id := ref
	if platform := opt.Platform; platform == nil {
		id += platforms.Format(platforms.DefaultSpec())
	} else {
		id += platforms.Format(*platform)
	}

	rec.Join(digest.FromString(id))

	return g.Client.ResolveImageConfig(ctx, ref, opt)
}

// Solve records the vertexes of the definition and frontend inputs as members
// of the current progress group, and calls the inner Solve.
func (g recordingGateway) Solve(ctx context.Context, opts bkgw.SolveRequest) (*bkgw.Result, error) {
	rec := progrock.RecorderFromContext(ctx)

	if opts.Definition != nil {
		recordVertexes(rec, opts.Definition)
	}

	for _, input := range opts.FrontendInputs {
		if input == nil {
			// TODO(vito): we currently pass a nil def to Dockerfile inputs, should
			// probably change that to llb.Scratch
			continue
		}

		recordVertexes(rec, input)
	}

	return g.Client.Solve(ctx, opts)
}

func recordVertexes(recorder *progrock.Recorder, def *pb.Definition) {
	dgsts := []digest.Digest{}
	for dgst, meta := range def.Metadata {
		_ = meta
		if meta.ProgressGroup != nil {
			if meta.ProgressGroup.Id != "" && meta.ProgressGroup.Id[0] == '[' {
				// Dagger progress group with pipeline.Path embedded
				// TODO(vito): remove this when we fully switch off of ProgressGroup
				dgsts = append(dgsts, dgst)
			} else {
				// Regular progress group, i.e. from Dockerfile; record it as a
				// subgroup.
				recorder.WithGroup(meta.ProgressGroup.Name).Join(dgst)
			}
		} else {
			dgsts = append(dgsts, dgst)
		}
	}

	recorder.Join(dgsts...)
}
