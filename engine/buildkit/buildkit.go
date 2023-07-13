package buildkit

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/engine/session"
	bkcache "github.com/moby/buildkit/cache"
	cacheutil "github.com/moby/buildkit/cache/util"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver"
	solvererror "github.com/moby/buildkit/solver/errdefs"
	llberror "github.com/moby/buildkit/solver/llbsolver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

const (
	// Exec errors will only include the last this number of bytes of output.
	MaxExecErrorOutputBytes = 100 * 1024

	// TruncationMessage is the message that will be prepended to truncated output.
	TruncationMessage = "[omitting %d bytes]..."

	// MaxFileContentsChunkSize sets the maximum chunk size for ReadFile calls
	// Equals around 95% of the max message size (16777216) in
	// order to keep space for any Protocol Buffers overhead:
	MaxFileContentsChunkSize = 15938355

	// MaxFileContentsSize sets the limit of the maximum file size
	// that can be retrieved using File.Contents, currently set to 128MB:
	MaxFileContentsSize = 128 << 20

	// MetaMountDestPath is the special path that the shim writes metadata to.
	MetaMountDestPath = "/.dagger_meta_mount"

	// MetaSourcePath is a world-writable directory created and mounted to /dagger.
	MetaSourcePath = "meta"
)

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	llbBridge        frontend.FrontendLLBBridge
	worker           worker.Worker
	sessionManager   *session.Manager
	cacheConfigType  string
	cacheConfigAttrs map[string]string

	refs   map[*ref]struct{}
	refsMu sync.Mutex
}

type Result = solverresult.Result[*ref]

func NewClient(
	llbBridge frontend.FrontendLLBBridge,
	worker worker.Worker,
	sessionManager *session.Manager,
	cacheConfigType string,
	cacheConfigAttrs map[string]string,
) *Client {
	return &Client{
		llbBridge:        llbBridge,
		worker:           worker,
		sessionManager:   sessionManager,
		cacheConfigType:  cacheConfigType,
		cacheConfigAttrs: cacheConfigAttrs,
		refs:             make(map[*ref]struct{}),
	}
}

func (c *Client) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *Result, rerr error) {
	ctx = withOutgoingContext(ctx)
	if c.cacheConfigType != "" {
		req.CacheImports = []bkgw.CacheOptionsEntry{{
			Type:  c.cacheConfigType,
			Attrs: c.cacheConfigAttrs,
		}}
	}

	llbRes, err := c.llbBridge.Solve(ctx, req, c.sessionManager.ID())
	if err != nil {
		return nil, wrapError(ctx, err, c.sessionManager.ID())
	}
	res, err := solverresult.ConvertResult(llbRes, func(rp solver.ResultProxy) (*ref, error) {
		return newRef(rp, c), nil
	})
	if err != nil {
		return nil, err
	}

	c.refsMu.Lock()
	defer c.refsMu.Unlock()
	if res.Ref != nil {
		c.refs[res.Ref] = struct{}{}
	}
	for _, rf := range res.Refs {
		c.refs[rf] = struct{}{}
	}
	return res, nil
}

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
	ctx = withOutgoingContext(ctx)
	return c.llbBridge.ResolveImageConfig(ctx, ref, opt)
}

func (c *Client) NewContainer(ctx context.Context, req bkgw.NewContainerRequest) (bkgw.Container, error) {
	ctx = withOutgoingContext(ctx)
	ctrReq := container.NewContainerRequest{
		ContainerID: identity.NewID(), // TODO: give a meaningful name?
		NetMode:     req.NetMode,
		Hostname:    req.Hostname,
		Mounts:      make([]container.Mount, len(req.Mounts)),
	}

	extraHosts, err := container.ParseExtraHosts(req.ExtraHosts)
	if err != nil {
		return nil, err
	}
	ctrReq.ExtraHosts = extraHosts

	// get the input mounts in parallel in case they need to be evaluated, which can be expensive
	eg, ctx := errgroup.WithContext(ctx)
	for i, m := range req.Mounts {
		i, m := i, m
		eg.Go(func() error {
			ref, ok := m.Ref.(*ref)
			if !ok {
				return fmt.Errorf("unexpected ref type: %T", m.Ref)
			}
			var workerRef *worker.WorkerRef
			if ref != nil {
				res, err := ref.resultProxy.Result(ctx)
				if err != nil {
					return err
				}
				var ok bool
				workerRef, ok = res.Sys().(*worker.WorkerRef)
				if !ok {
					return fmt.Errorf("invalid res: %T", res.Sys())
				}
			}
			ctrReq.Mounts[i] = container.Mount{
				WorkerRef: workerRef,
				Mount: &pb.Mount{
					Dest:      m.Dest,
					Selector:  m.Selector,
					Readonly:  m.Readonly,
					MountType: m.MountType,
					CacheOpt:  m.CacheOpt,
					SecretOpt: m.SecretOpt,
					SSHOpt:    m.SSHOpt,
				},
			}
			return nil
		})
	}
	err = eg.Wait()
	if err != nil {
		return nil, err
	}

	ctr, err := c.sessionManager.NewContainer(ctx, ctrReq)
	if err != nil {
		return nil, err
	}
	// TODO: cleanup containers at end of session, if that doesn't happen automatically already
	return ctr, nil
}

// CombinedResult returns a buildkit result with all the refs solved by this client so far.
// This is useful for constructing a result for remote caching.
func (c *Client) CombinedResult(ctx context.Context) (*Result, error) {
	c.refsMu.Lock()
	defer c.refsMu.Unlock()

	mergeInputs := make([]llb.State, 0, len(c.refs))
	for r := range c.refs {
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
	return c.Solve(ctx, bkgw.SolveRequest{
		Definition: llbdef.ToPB(),
	})
}

// llb that should be used for local dir imports
func (c *Client) LocalLLB(ctx context.Context, path string, opts ...llb.LocalOption) (llb.State, error) {
	name, err := c.sessionManager.LocalLLBName(ctx, path)
	if err != nil {
		return llb.State{}, err
	}
	opts = append(opts,
		// synchronize concurrent filesyncs for the same path
		llb.SharedKeyHint(name),
		// we specify our internal session ID so it goes through our proxies that verify requesters
		// have access and then route it to the correct client
		llb.SessionID(c.sessionManager.ID()),
	)
	return llb.Local(name, opts...), nil
}

func (c *Client) LocalExport(
	ctx context.Context,
	def *pb.Definition,
	destPath string,
) error {
	res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: def})
	if err != nil {
		return fmt.Errorf("failed to solve for local export: %s", err)
	}

	cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
		cachedRes, err := rf.Result(ctx)
		if err != nil {
			return nil, err
		}
		workerRef, ok := cachedRes.Sys().(*worker.WorkerRef)
		if !ok {
			return nil, fmt.Errorf("invalid ref: %T", cachedRes.Sys())
		}
		return workerRef.ImmutableRef, nil
	})
	if err != nil {
		return fmt.Errorf("failed to convert result: %s", err)
	}

	return c.sessionManager.LocalExport(ctx, cacheRes, destPath)
}

// TODO: Actually call this when the server instance ends
// TODO: Integ test for all cache being releasable at end of every integ test suite
func (c *Client) Close() error {
	// TODO: release any running interactive containers
	for rf := range c.refs {
		if rf != nil {
			rf.resultProxy.Release(context.TODO())
		}
	}
	return nil
}

func newRef(res solver.ResultProxy, c *Client) *ref {
	return &ref{
		resultProxy: res,
		c:           c,
	}
}

type ref struct {
	resultProxy solver.ResultProxy
	c           *Client
}

func (r *ref) ToState() (llb.State, error) {
	def := r.resultProxy.Definition()
	if def.Def == nil {
		return llb.Scratch(), nil
	}
	defOp, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(defOp), nil
}

func (r *ref) Evaluate(ctx context.Context) error {
	_, err := r.Result(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (r *ref) ReadFile(ctx context.Context, req bkgw.ReadRequest) ([]byte, error) {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return nil, err
	}
	cacheReq := cacheutil.ReadRequest{
		Filename: req.Filename,
	}
	if r := req.Range; r != nil {
		cacheReq.Range = &cacheutil.FileRange{
			Offset: r.Offset,
			Length: r.Length,
		}
	}
	return cacheutil.ReadFile(ctx, mnt, cacheReq)
}

func (r *ref) ReadDir(ctx context.Context, req bkgw.ReadDirRequest) ([]*fstypes.Stat, error) {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return nil, err
	}
	cacheReq := cacheutil.ReadDirRequest{
		Path:           req.Path,
		IncludePattern: req.IncludePattern,
	}
	return cacheutil.ReadDir(ctx, mnt, cacheReq)
}

func (r *ref) StatFile(ctx context.Context, req bkgw.StatRequest) (*fstypes.Stat, error) {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return nil, err
	}
	return cacheutil.StatFile(ctx, mnt, req.Path)
}

func (r *ref) getMountable(ctx context.Context) (snapshot.Mountable, error) {
	res, err := r.Result(ctx)
	if err != nil {
		return nil, err
	}
	workerRef, ok := res.Sys().(*worker.WorkerRef)
	if !ok {
		return nil, fmt.Errorf("invalid ref: %T", res.Sys())
	}
	return workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(r.c.sessionManager.ID()))
}

func (r *ref) Result(ctx context.Context) (solver.CachedResult, error) {
	ctx = withOutgoingContext(ctx)
	res, err := r.resultProxy.Result(ctx)
	if err != nil {
		return nil, wrapError(ctx, err, r.c.sessionManager.ID())
	}
	return res, nil
}

func wrapError(ctx context.Context, baseErr error, sessionID string) error {
	var fileErr *llberror.FileActionError
	if errors.As(baseErr, &fileErr) {
		return solvererror.WithSolveError(baseErr, fileErr.ToSubject(), nil, nil)
	}

	var slowCacheErr *solver.SlowCacheError
	if errors.As(baseErr, &slowCacheErr) {
		// TODO: include input IDs? Or does that not matter for us?
		return solvererror.WithSolveError(baseErr, slowCacheErr.ToSubject(), nil, nil)
	}

	var execErr *llberror.ExecError
	if !errors.As(baseErr, &execErr) {
		return baseErr
	}

	var opErr *solvererror.OpError
	if !errors.As(baseErr, &opErr) {
		return baseErr
	}
	op := opErr.Op
	if op == nil || op.Op == nil {
		return baseErr
	}
	execOp, ok := op.Op.(*pb.Op_Exec)
	if !ok {
		return baseErr
	}

	// This was an exec error, we will retrieve the exec's output and include
	// it in the error message

	// get the mnt corresponding to the metadata where stdout/stderr are stored
	// TODO: support redirected stdout/stderr again too, maybe just have shim write to both?
	var metaMountResult solver.Result
	for i, mnt := range execOp.Exec.Mounts {
		if mnt.Dest == MetaMountDestPath {
			metaMountResult = execErr.Mounts[i]
			break
		}
	}
	if metaMountResult == nil {
		return baseErr
	}

	workerRef, ok := metaMountResult.Sys().(*worker.WorkerRef)
	if !ok {
		return errors.Join(baseErr, fmt.Errorf("invalid ref type: %T", metaMountResult.Sys()))
	}
	mntable, err := workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(sessionID))
	if err != nil {
		return errors.Join(err, baseErr)
	}

	stdoutBytes, err := getExecMetaFile(ctx, mntable, "stdout")
	if err != nil {
		return errors.Join(err, baseErr)
	}
	stderrBytes, err := getExecMetaFile(ctx, mntable, "stderr")
	if err != nil {
		return errors.Join(err, baseErr)
	}

	exitCodeBytes, err := getExecMetaFile(ctx, mntable, "exitCode")
	if err != nil {
		return errors.Join(err, baseErr)
	}
	exitCode := -1
	if len(exitCodeBytes) > 0 {
		exitCode, err = strconv.Atoi(string(exitCodeBytes))
		if err != nil {
			return errors.Join(err, baseErr)
		}
	}

	return &ExecError{
		original: baseErr,
		Cmd:      execOp.Exec.Meta.Args,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(string(stdoutBytes)),
		Stderr:   strings.TrimSpace(string(stderrBytes)),
	}
}

func getExecMetaFile(ctx context.Context, mntable snapshot.Mountable, fileName string) ([]byte, error) {
	ctx = withOutgoingContext(ctx)
	filePath := path.Join(MetaSourcePath, fileName)
	stat, err := cacheutil.StatFile(ctx, mntable, filePath)
	if err != nil {
		// TODO: would be better to verify this is a "not exists" error, return err if not
		bklog.G(ctx).Debugf("getExecMetaFile: failed to stat file: %v", err)
		return nil, nil
	}

	req := cacheutil.ReadRequest{
		Filename: filePath,
		Range: &cacheutil.FileRange{
			Length: int(stat.Size_),
		},
	}
	if req.Range.Length > MaxExecErrorOutputBytes {
		// TODO: re-add truncation message
		req.Range.Offset = int(stat.Size_) - MaxExecErrorOutputBytes
		req.Range.Length = MaxExecErrorOutputBytes
	}
	return cacheutil.ReadFile(ctx, mntable, req)
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
