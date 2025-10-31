package buildkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/cache/contenthash"
	cacheutil "github.com/dagger/dagger/internal/buildkit/cache/util"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	bksolver "github.com/dagger/dagger/internal/buildkit/solver"
	solvererror "github.com/dagger/dagger/internal/buildkit/solver/errdefs"
	llberror "github.com/dagger/dagger/internal/buildkit/solver/llbsolver/errdefs"
	"github.com/dagger/dagger/internal/buildkit/solver/llbsolver/provenance"
	solverresult "github.com/dagger/dagger/internal/buildkit/solver/result"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	bkworker "github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
)

const (
	// Exec errors will only include the last this number of bytes of output.
	MaxExecErrorOutputBytes = 100 * 1024

	// TruncationMessage is the message that will be prepended to truncated output.
	TruncationMessage = "[omitting %d bytes]..."

	// MaxFileContentsChunkSize sets the maximum chunk size for ReadFile calls
	// Equals around 95% of the max message size (4MB) in
	// order to keep space for any Protocol Buffers overhead:
	MaxFileContentsChunkSize = 3984588

	// MaxFileContentsSize sets the limit of the maximum file size
	// that can be retrieved using File.Contents, currently set to 128MB:
	MaxFileContentsSize = 128 << 20

	// MetaMountDestPath is the special path that the shim writes metadata to.
	MetaMountDestPath           = "/.dagger_meta_mount"
	MetaMountExitCodePath       = "exitCode"
	MetaMountStdoutPath         = "stdout"
	MetaMountStderrPath         = "stderr"
	MetaMountCombinedOutputPath = "combinedOutput"
	MetaMountClientIDPath       = "clientID"
)

type Result = solverresult.Result[*ref]

type Reference interface {
	bkgw.Reference
	Release(context.Context) error
}

func newRef(res bksolver.ResultProxy, c *Client) *ref {
	return &ref{
		resultProxy: res,
		c:           c,
	}
}

type ref struct {
	resultProxy bksolver.ResultProxy
	c           *Client
}

func (r *ref) ToState() (llb.State, error) {
	if r == nil {
		return llb.Scratch(), nil
	}
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

func (r *ref) Digest(ctx context.Context, path string) (digest.Digest, error) {
	if r == nil {
		return contenthash.Checksum(ctx, nil, path, contenthash.ChecksumOpts{}, nil)
	}
	cacheRef, err := r.CacheRef(ctx)
	if err != nil {
		return "", err
	}
	sessionGroup := bksession.NewGroup(r.c.ID())
	return contenthash.Checksum(ctx, cacheRef, path, contenthash.ChecksumOpts{}, sessionGroup)
}

func (r *ref) Evaluate(ctx context.Context) error {
	if r == nil {
		return nil
	}
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

func (r *ref) WalkDir(ctx context.Context, req WalkDirRequest) error {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return err
	}
	// cacheutil.WalkDir isn't a thing (so we'll just call our own)
	return walkDir(ctx, mnt, req)
}

func (r *ref) Mount(ctx context.Context, f func(path string) error) error {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return err
	}
	return withMount(mnt, f)
}

func (r *ref) StatFile(ctx context.Context, req bkgw.StatRequest) (*fstypes.Stat, error) {
	ctx = withOutgoingContext(ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return nil, err
	}
	return cacheutil.StatFile(ctx, mnt, req.Path)
}

func (r *ref) AddDependencyBlobs(ctx context.Context, blobs map[digest.Digest]*ocispecs.Descriptor) error {
	ctx = withOutgoingContext(ctx)

	cacheRef, err := r.CacheRef(ctx)
	if err != nil {
		return err
	}

	// Finalize ensures that there isn't an equalMutable with a different ID and thus different lease. It shouldn't
	// be called on a ref that actually benefits from having an equalMutable, but that's really only a local dir
	// sync ref.
	err = cacheRef.Finalize(ctx)
	if err != nil {
		return err
	}

	// This relies on the lease ID being the ref ID which, while unlikely to change, is worth
	// keeping in mind:
	// https://github.com/dagger/dagger/internal/buildkit/blob/c3c65787b5e2c2c9fcab1d0b9bd1884a37384c90/cache/manager.go#L231
	leaseID := cacheRef.ID()

	lm := r.c.Worker.LeaseManager()
	for blobDigest := range blobs {
		err := lm.AddResource(ctx, leases.Lease{ID: leaseID}, leases.Resource{
			ID:   blobDigest.String(),
			Type: "content",
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ref) getMountable(ctx context.Context) (snapshot.Mountable, error) {
	if r == nil {
		return nil, nil
	}
	res, err := r.Result(ctx)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	workerRef, ok := res.Sys().(*bkworker.WorkerRef)
	if !ok {
		return nil, fmt.Errorf("invalid ref: %T", res.Sys())
	}
	if workerRef == nil || workerRef.ImmutableRef == nil {
		return nil, nil
	}
	return workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(r.c.ID()))
}

func (r *ref) Result(ctx context.Context) (bksolver.CachedResult, error) {
	if r == nil {
		return nil, nil
	}
	ctx = withOutgoingContext(ctx)
	res, err := r.resultProxy.Result(ctx)
	if err != nil {
		// writing log w/ %+v so that we can see stack traces embedded in err by buildkit's usage of pkg/errors
		bklog.G(ctx).
			WithField("caller stack", string(debug.Stack())).
			Errorf("ref evaluate error: %+v", err)
		err = includeBuildkitContextCancelledLine(err)
		return nil, WrapError(ctx, err, r.c)
	}
	return res, nil
}

func (r *ref) Provenance() *provenance.Capture {
	if r == nil {
		return nil
	}
	pr := r.resultProxy.Provenance()
	if pr == nil {
		return nil
	}
	p, ok := pr.(*provenance.Capture)
	if !ok {
		return nil
	}
	return p
}

func (r *ref) CacheRef(ctx context.Context) (bkcache.ImmutableRef, error) {
	cacheRes, err := r.Result(ctx)
	if err != nil {
		return nil, err
	}
	workerRef, ok := cacheRes.Sys().(*bkworker.WorkerRef)
	if !ok {
		return nil, fmt.Errorf("invalid ref: %T", cacheRes.Sys())
	}
	return workerRef.ImmutableRef, nil
}

func (r *ref) Release(ctx context.Context) error {
	if r == nil {
		return nil
	}
	return r.resultProxy.Release(ctx)
}

func ConvertToWorkerCacheResult(ctx context.Context, res *solverresult.Result[*ref]) (*solverresult.Result[bkcache.ImmutableRef], error) {
	return solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
		res, err := rf.Result(ctx)
		if err != nil {
			return nil, err
		}
		workerRef, ok := res.Sys().(*bkworker.WorkerRef)
		if !ok {
			return nil, fmt.Errorf("invalid ref: %T", res.Sys())
		}
		return workerRef.ImmutableRef, nil
	})
}

func WrapError(ctx context.Context, baseErr error, client *Client) error {
	var slowCacheErr *bksolver.SlowCacheError
	if errors.As(baseErr, &slowCacheErr) {
		if slowCacheErr.Result != nil {
			defer slowCacheErr.Result.Release(context.Background())
		}
		// TODO: include input IDs? Or does that not matter for us?
		return solvererror.WithSolveError(baseErr, slowCacheErr.ToSubject(), nil, nil)
	}

	var execErr *llberror.ExecError
	if errors.As(baseErr, &execErr) {
		defer func() {
			execErr.Release()
			execErr.OwnerBorrowed = true
		}()
	}

	var fileErr *llberror.FileActionError
	if errors.As(baseErr, &fileErr) {
		return solvererror.WithSolveError(baseErr, fileErr.ToSubject(), nil, nil)
	}

	var ierr RichError
	if errors.As(baseErr, &ierr) {
		if ierr.Meta == nil {
			return baseErr
		}
		if err := ierr.DebugTerminal(ctx, client); err != nil {
			return err
		}

		execErr, ok, err := ierr.AsExecErr(ctx, client)
		if err != nil {
			return errors.Join(err, baseErr)
		}
		if ok {
			return execErr
		}
	}

	return baseErr
}

func ReadSnapshotPath(ctx context.Context, c *Client, mntable snapshot.Mountable, filePath string, limit int) ([]byte, error) {
	ctx = withOutgoingContext(ctx)
	stat, err := cacheutil.StatFile(ctx, mntable, filePath)
	if err != nil {
		// TODO: would be better to verify this is a "not exists" error, return err if not
		bklog.G(ctx).Debugf("ReadSnapshotPath: failed to stat file: %v", err)
		return nil, nil
	}

	req := cacheutil.ReadRequest{
		Filename: filePath,
		Range: &cacheutil.FileRange{
			Length: int(stat.Size_),
		},
	}

	if limit != -1 && req.Range.Length > limit {
		req.Range.Offset = int(stat.Size_) - limit
		req.Range.Length = limit
	}
	contents, err := cacheutil.ReadFile(ctx, mntable, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", filePath, err)
	}
	if limit != -1 && len(contents) >= limit {
		truncMsg := fmt.Sprintf(TruncationMessage, int(stat.Size_)-limit)
		copy(contents, truncMsg)
	}
	return contents, nil
}

type WalkDirRequest struct {
	Path           string
	IncludePattern string
	Callback       func(path string, info os.FileInfo) error
}

// walkDir is inspired by cacheutil.ReadDir, but instead executes a callback on
// every item in the fs
func walkDir(ctx context.Context, mount snapshot.Mountable, req WalkDirRequest) error {
	if req.Callback == nil {
		return nil
	}

	var fo fsutil.FilterOpt
	if req.IncludePattern != "" {
		fo.IncludePatterns = append(fo.IncludePatterns, req.IncludePattern)
	}

	return withMount(mount, func(root string) error {
		fp, err := fs.RootPath(root, req.Path)
		if err != nil {
			return err
		}
		return fsutil.Walk(ctx, fp, &fo, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return fmt.Errorf("walking %q: %w", root, err)
			}
			return req.Callback(path, info)
		})
	})
}

// withMount is copied directly from buildkit
func withMount(mount snapshot.Mountable, cb func(string) error) error {
	lm := snapshot.LocalMounter(mount)

	root, err := lm.Mount()
	if err != nil {
		return err
	}

	defer func() {
		if lm != nil {
			lm.Unmount()
		}
	}()

	if err := cb(root); err != nil {
		return err
	}

	if err := lm.Unmount(); err != nil {
		return err
	}
	lm = nil
	return nil
}

// buildkit only sets context cancelled cause errors to "context cancelled" +
// embedded stack traces from the github.com/pkg/errors library. That library
// only lets you see the stack trace if you print the error with %+v, so we
// try doing that, finding a context cancelled error and parsing out the line
// number that caused the error, including that in the error message so when users
// hit this we can have a chance of debugging without needing to request their
// full engine logs.
// Related to https://github.com/dagger/dagger/issues/7699
func includeBuildkitContextCancelledLine(err error) error {
	errStrWithStack := fmt.Sprintf("%+v", err)
	errStrSplit := strings.Split(errStrWithStack, "\n")
	for i, errStrLine := range errStrSplit {
		if errStrLine != "context canceled" {
			continue
		}
		lineNoIndex := i + 2
		if lineNoIndex >= len(errStrSplit) {
			break
		}
		lineNoLine := errStrSplit[lineNoIndex]
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(lineNoLine))
		break
	}
	return err
}
