package buildkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/containerd/containerd/leases"
	"github.com/containerd/continuity/fs"
	"github.com/dagger/dagger/dagql/idtui"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/cache/contenthash"
	cacheutil "github.com/moby/buildkit/cache/util"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkgwpb "github.com/moby/buildkit/frontend/gateway/pb"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	bksolver "github.com/moby/buildkit/solver"
	solvererror "github.com/moby/buildkit/solver/errdefs"
	llberror "github.com/moby/buildkit/solver/llbsolver/errdefs"
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	"github.com/moby/buildkit/util/bklog"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
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
	MetaMountDestPath     = "/.dagger_meta_mount"
	MetaMountExitCodePath = "exitCode"
	MetaMountStdinPath    = "stdin"
	MetaMountStdoutPath   = "stdout"
	MetaMountStderrPath   = "stderr"
	MetaMountClientIDPath = "clientID"
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
	ctx = withOutgoingContext(r.c, ctx)
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
	ctx = withOutgoingContext(r.c, ctx)
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
	ctx = withOutgoingContext(r.c, ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return err
	}
	// cacheutil.WalkDir isn't a thing (so we'll just call our own)
	return walkDir(ctx, mnt, req)
}

func (r *ref) StatFile(ctx context.Context, req bkgw.StatRequest) (*fstypes.Stat, error) {
	ctx = withOutgoingContext(r.c, ctx)
	mnt, err := r.getMountable(ctx)
	if err != nil {
		return nil, err
	}
	return cacheutil.StatFile(ctx, mnt, req.Path)
}

func (r *ref) AddDependencyBlobs(ctx context.Context, blobs map[digest.Digest]*ocispecs.Descriptor) error {
	ctx = withOutgoingContext(r.c, ctx)

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
	// https://github.com/moby/buildkit/blob/c3c65787b5e2c2c9fcab1d0b9bd1884a37384c90/cache/manager.go#L231
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
	ctx = withOutgoingContext(r.c, ctx)
	res, err := r.resultProxy.Result(ctx)
	if err != nil {
		// writing log w/ %+v so that we can see stack traces embedded in err by buildkit's usage of pkg/errors
		bklog.G(ctx).Errorf("ref evaluate error: %+v", err)
		err = includeBuildkitContextCancelledLine(err)
		return nil, wrapError(ctx, err, r.c)
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

func wrapError(ctx context.Context, baseErr error, client *Client) error {
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

	if execErr == nil {
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
	execOp, ok := op.Op.(*bksolverpb.Op_Exec)
	if !ok {
		return baseErr
	}

	// This was an exec error, we will retrieve the exec's output and include
	// it in the error message

	// get the mnt corresponding to the metadata where stdout/stderr are stored
	var metaMountResult bksolver.Result
	for i, mnt := range execOp.Exec.Mounts {
		if mnt.Dest == MetaMountDestPath {
			metaMountResult = execErr.Mounts[i]
			break
		}
	}
	if metaMountResult == nil {
		return baseErr
	}

	workerRef, ok := metaMountResult.Sys().(*bkworker.WorkerRef)
	if !ok {
		return errors.Join(baseErr, fmt.Errorf("invalid ref type: %T", metaMountResult.Sys()))
	}
	mntable, err := workerRef.ImmutableRef.Mount(ctx, true, bksession.NewGroup(client.ID()))
	if err != nil {
		return errors.Join(err, baseErr)
	}

	stdoutBytes, err := getExecMetaFile(ctx, client, mntable, MetaMountStdoutPath)
	if err != nil {
		return errors.Join(err, baseErr)
	}
	stderrBytes, err := getExecMetaFile(ctx, client, mntable, MetaMountStderrPath)
	if err != nil {
		return errors.Join(err, baseErr)
	}
	exitCodeBytes, err := getExecMetaFile(ctx, client, mntable, MetaMountExitCodePath)
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

	// Start a debug container if the exec failed
	if err := debugContainer(ctx, execOp.Exec, execErr, opErr, client); err != nil {
		bklog.G(ctx).Debugf("debug terminal error: %v", err)
	}

	return &ExecError{
		original: baseErr,
		Cmd:      execOp.Exec.Meta.Args,
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(string(stdoutBytes)),
		Stderr:   strings.TrimSpace(string(stderrBytes)),
	}
}

func debugContainer(ctx context.Context, execOp *bksolverpb.ExecOp, execErr *llberror.ExecError, opErr *solvererror.OpError, client *Client) error {
	if !client.Opts.Interactive {
		return nil
	}

	execMd, ok, err := ExecutionMetadataFromDescription(opErr.Description)
	if err != nil {
		return fmt.Errorf("failed to retrieve execution metadata: %w", err)
	}
	if !ok {
		// containers created by buildkit internals like the dockerfile frontend
		return nil
	}

	// Ensure we only spawn one terminal per exec.
	if execMd.ExecID != "" {
		if _, exists := client.execMap.LoadOrStore(execMd.ExecID, struct{}{}); exists {
			return nil
		}
	}

	// If this is the (internal) exec of the module itself, we don't want to spawn a terminal.
	if execMd.Internal {
		return nil
	}

	// relevant buildkit code we need to contend with here:
	// https://github.com/moby/buildkit/blob/44504feda1ce39bb8578537a6e6a93f90bdf4220/solver/llbsolver/ops/exec.go#L386-L409
	mounts := []ContainerMount{}
	for i, m := range execOp.Mounts {
		if m.Input == -1 {
			mounts = append(mounts, ContainerMount{
				Mount: &bkgw.Mount{
					Dest:      m.Dest,
					Selector:  m.Selector,
					Readonly:  m.Readonly,
					MountType: m.MountType,
					CacheOpt:  m.CacheOpt,
					SecretOpt: m.SecretOpt,
					SSHOpt:    m.SSHOpt,
				},
			})
			continue
		}

		// sanity check we don't panic
		if i >= len(execErr.Mounts) {
			return fmt.Errorf("exec error mount index out of bounds: %d", i)
		}
		errMnt := execErr.Mounts[i]
		if errMnt == nil {
			continue
		}
		workerRef, ok := errMnt.Sys().(*bkworker.WorkerRef)
		if !ok {
			continue
		}

		mounts = append(mounts, ContainerMount{
			WorkerRef: workerRef,
			Mount: &bkgw.Mount{
				Dest:      m.Dest,
				Selector:  m.Selector,
				Readonly:  m.Readonly,
				MountType: m.MountType,
				CacheOpt:  m.CacheOpt,
				SecretOpt: m.SecretOpt,
				SSHOpt:    m.SSHOpt,
				ResultID:  errMnt.ID(),
			},
		})
	}

	dbgCtr, err := client.NewContainer(ctx, NewContainerRequest{
		Hostname: execOp.Meta.Hostname,
		Mounts:   mounts,
	})
	if err != nil {
		return err
	}
	term, err := client.OpenTerminal(ctx)
	if err != nil {
		return err
	}
	// always close term; it's wrapped in a once so it won't be called multiple times
	defer term.Close(bkgwpb.UnknownExitStatus)

	output := idtui.NewOutput(term.Stderr)
	fmt.Fprint(term.Stderr,
		output.String(idtui.IconFailure).Foreground(termenv.ANSIRed).String()+" Exec failed, attaching terminal: ")
	dump := idtui.Dump{Newline: "\r\n", Prefix: "    "}
	fmt.Fprint(term.Stderr, dump.Newline)
	if err := dump.DumpID(output, execMd.CallID); err != nil {
		return fmt.Errorf("failed to serialize service ID: %w", err)
	}
	fmt.Fprint(term.Stderr, dump.Newline)
	fmt.Fprintf(term.Stderr,
		output.String("! %s").Foreground(termenv.ANSIYellow).String(), execErr.Error())
	fmt.Fprint(term.Stderr, dump.Newline)

	// We default to "/bin/sh" if the client doesn't provide a command.
	debugCommand := []string{"/bin/sh"}
	if len(client.Opts.InteractiveCommand) > 0 {
		debugCommand = client.Opts.InteractiveCommand
	}

	eg, ctx := errgroup.WithContext(ctx)

	dbgShell, err := dbgCtr.Start(ctx, bkgw.StartRequest{
		Args: debugCommand,

		Env:          execOp.Meta.Env,
		Cwd:          execOp.Meta.Cwd,
		User:         execOp.Meta.User,
		SecurityMode: execOp.Security,
		SecretEnv:    execOp.Secretenv,

		Tty:    true,
		Stdin:  term.Stdin,
		Stdout: term.Stdout,
		Stderr: term.Stderr,
	})
	if err != nil {
		return err
	}

	eg.Go(func() error {
		err := <-term.ErrCh
		if err != nil {
			return fmt.Errorf("terminal error: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		for resize := range term.ResizeCh {
			err := dbgShell.Resize(ctx, resize)
			if err != nil {
				return fmt.Errorf("failed to resize terminal: %w", err)
			}
		}
		return nil
	})
	eg.Go(func() error {
		waitErr := dbgShell.Wait()
		termExitCode := 0
		if waitErr != nil {
			termExitCode = 1
			var exitErr *bkgwpb.ExitError
			if errors.As(waitErr, &exitErr) {
				termExitCode = int(exitErr.ExitCode)
			}
		}

		return term.Close(termExitCode)
	})

	return eg.Wait()
}

func getExecMetaFile(ctx context.Context, c *Client, mntable snapshot.Mountable, fileName string) ([]byte, error) {
	return ReadSnapshotPath(ctx, c, mntable, path.Join(MetaMountDestPath, fileName))
}

func ReadSnapshotPath(ctx context.Context, c *Client, mntable snapshot.Mountable, filePath string) ([]byte, error) {
	ctx = withOutgoingContext(c, ctx)
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

	if req.Range.Length > MaxExecErrorOutputBytes {
		req.Range.Offset = int(stat.Size_) - MaxExecErrorOutputBytes
		req.Range.Length = MaxExecErrorOutputBytes
	}
	contents, err := cacheutil.ReadFile(ctx, mntable, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", filePath, err)
	}
	if len(contents) >= MaxExecErrorOutputBytes {
		truncMsg := fmt.Sprintf(TruncationMessage, int(stat.Size_)-MaxExecErrorOutputBytes)
		copy(contents, truncMsg)
	}
	return contents, nil
}

type WalkDirRequest struct {
	Path           string
	IncludePattern string
	Callback       func(path string, info *fstypes.Stat) error
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
			stat, ok := info.Sys().(*fstypes.Stat)
			if !ok {
				// This "can't happen(tm)".
				return fmt.Errorf("expected a *fsutil.Stat but got %T", info.Sys())
			}
			return req.Callback(path, stat)
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
