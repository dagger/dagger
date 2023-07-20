package buildkit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dagger/dagger/engine"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkfrontend "github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

type Opts struct {
	Worker         bkworker.Worker
	SessionManager *bksession.Manager
	LLBSolver      *llbsolver.Solver
	GenericSolver  *bksolver.Solver
}

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	Opts
	session   *bksession.Session
	job       *bksolver.Job
	llbBridge bkfrontend.FrontendLLBBridge

	refs         map[*ref]struct{}
	refsMu       sync.Mutex
	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex
}

type Result = solverresult.Result[*ref]

func NewClient(ctx context.Context, opts Opts) (*Client, error) {
	client := &Client{
		Opts:       opts,
		refs:       make(map[*ref]struct{}),
		containers: make(map[bkgw.Container]struct{}),
	}

	session, err := client.newSession(ctx)
	if err != nil {
		return nil, err
	}
	client.session = session

	job, err := client.GenericSolver.NewJob(client.ID())
	if err != nil {
		return nil, err
	}
	client.job = job
	client.job.SessionID = client.ID()

	// TODO: entitlements based on engine config

	client.llbBridge = client.LLBSolver.Bridge(client.job)

	return client, nil
}

func (c *Client) ID() string {
	// TODO: ? if you change this, be sure to change the session ID provide to llbBridge methods too
	return c.session.ID()
}

// TODO: Integ test for all cache being releasable at end of every integ test suite
func (c *Client) Close() error {
	c.job.Discard()
	c.job.CloseProgress()

	c.refsMu.Lock()
	for rf := range c.refs {
		if rf != nil {
			rf.resultProxy.Release(context.Background())
		}
	}
	c.refs = nil // TODO: make sure everything else handles this in case there's so other request happening for some reason and we don't get a panic. Or maybe just have a RWMutex for checks whether we have closed everywhere
	c.refsMu.Unlock()

	c.containersMu.Lock()
	for ctr := range c.containers {
		if ctr != nil {
			// TODO: can this block a long time on accident? should we have a timeout?
			ctr.Release(context.Background())
		}
	}
	c.containers = nil // TODO: same as above about handling this
	c.containersMu.Unlock()

	// TODO: ensure session is fully closed by goroutines started in client.newSession

	return nil
}

func (c *Client) Solve(ctx context.Context, req bkgw.SolveRequest) (_ *Result, rerr error) {
	ctx = withOutgoingContext(ctx)

	llbRes, err := c.llbBridge.Solve(ctx, req, c.ID())
	if err != nil {
		return nil, wrapError(ctx, err, c.ID())
	}
	res, err := solverresult.ConvertResult(llbRes, func(rp bksolver.ResultProxy) (*ref, error) {
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

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (string, digest.Digest, []byte, error) {
	ctx = withOutgoingContext(ctx)
	return c.llbBridge.ResolveImageConfig(ctx, ref, opt)
}

func (c *Client) NewContainer(ctx context.Context, req bkgw.NewContainerRequest) (bkgw.Container, error) {
	ctx = withOutgoingContext(ctx)
	ctrReq := bkcontainer.NewContainerRequest{
		ContainerID: identity.NewID(), // TODO: give a meaningful name?
		NetMode:     req.NetMode,
		Hostname:    req.Hostname,
		Mounts:      make([]bkcontainer.Mount, len(req.Mounts)),
	}

	extraHosts, err := bkcontainer.ParseExtraHosts(req.ExtraHosts)
	if err != nil {
		return nil, err
	}
	ctrReq.ExtraHosts = extraHosts

	// get the input mounts in parallel in case they need to be evaluated, which can be expensive
	eg, egctx := errgroup.WithContext(ctx)
	for i, m := range req.Mounts {
		i, m := i, m
		eg.Go(func() error {
			var workerRef *bkworker.WorkerRef
			if m.Ref != nil {
				ref, ok := m.Ref.(*ref)
				if !ok {
					return fmt.Errorf("dagger: unexpected ref type: %T", m.Ref)
				}
				if ref == nil {
					panic("huh? ref")
				}
				if ref.resultProxy == nil {
					panic("huh? resultProxy")
				}
				res, err := ref.resultProxy.Result(egctx)
				if err != nil {
					return fmt.Errorf("result: %w", err)
				}
				workerRef, ok = res.Sys().(*bkworker.WorkerRef)
				if !ok {
					return fmt.Errorf("invalid res: %T", res.Sys())
				}
			}
			ctrReq.Mounts[i] = bkcontainer.Mount{
				WorkerRef: workerRef,
				Mount: &bksolverpb.Mount{
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
		return nil, fmt.Errorf("wait: %w", err)
	}

	// using context.Background so it continues running until exit or when c.Close() is called
	ctr, err := bkcontainer.NewContainer(
		context.Background(),
		c.Worker,
		c.SessionManager,
		bksession.NewGroup(c.ID()),
		ctrReq,
	)
	if err != nil {
		return nil, err
	}

	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	c.containers[ctr] = struct{}{}
	return ctr, nil
}

// CombinedResult returns a buildkit result with all the refs solved by this client so far.
// This is useful for constructing a result for upstream remote caching.
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

func (c *Client) WriteStatusesTo(ctx context.Context, ch chan *client.SolveStatus) error {
	return c.job.Status(ctx, ch)
}

type localImportOpts struct {
	OwnerClientID string `json:"ownerClientID,omitempty"`
	Path          string `json:"path,omitempty"`
}

func (c *Client) LocalImportLLB(ctx context.Context, path string, opts ...llb.LocalOption) (llb.State, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return llb.State{}, err
	}

	nameBytes, err := json.Marshal(localImportOpts{
		// TODO: for now, the requester is always the owner of the local dir
		// when the dir is initially created in LLB (i.e. you can't request a
		// a new local dir from another session, you can only be passed one
		// from another session already created).
		OwnerClientID: clientMetadata.ClientID,
		Path:          path,
	})
	if err != nil {
		return llb.State{}, err
	}
	name := base64.URLEncoding.EncodeToString(nameBytes)

	opts = append(opts,
		// synchronize concurrent filesyncs for the same path
		llb.SharedKeyHint(name),
		llb.SessionID(c.ID()),
	)
	return llb.Local(name, opts...), nil
}

func (c *Client) LocalExport(
	ctx context.Context,
	def *bksolverpb.Definition,
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
		workerRef, ok := cachedRes.Sys().(*bkworker.WorkerRef)
		if !ok {
			return nil, fmt.Errorf("invalid ref: %T", cachedRes.Sys())
		}
		return workerRef.ImmutableRef, nil
	})
	if err != nil {
		return fmt.Errorf("failed to convert result: %s", err)
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterLocal, c.SessionManager)
	if err != nil {
		return err
	}

	expInstance, err := exporter.Resolve(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to resolve exporter: %s", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %s", err)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md[engine.LocalDirExportDestClientIDMetaKey] = []string{clientMetadata.ClientID}
	md[engine.LocalDirExportDestPathMetaKey] = []string{destPath}

	ctx = metadata.NewOutgoingContext(ctx, md)

	// TODO: could optimize by just calling relevant session methods directly, not
	// all that much code involved
	_, _, err = expInstance.Export(ctx, cacheRes, c.ID())
	if err != nil {
		return fmt.Errorf("failed to export: %s", err)
	}
	return nil
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
