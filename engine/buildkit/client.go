package buildkit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/moby/buildkit/cache"
	"github.com/moby/buildkit/client"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkfrontend "github.com/moby/buildkit/frontend"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	bksecrets "github.com/moby/buildkit/session/secrets"
	bksolver "github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver"
	bksolverpb "github.com/moby/buildkit/solver/pb"
	solverresult "github.com/moby/buildkit/solver/result"
	bkworker "github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"
)

type Opts struct {
	Worker         bkworker.Worker
	SessionManager *bksession.Manager
	LLBSolver      *llbsolver.Solver
	GenericSolver  *bksolver.Solver
	SecretStore    bksecrets.SecretStore
	AuthProvider   *auth.RegistryAuthProvider
	// TODO: give precise definition
	MainClientCaller bksession.Caller
}

// Client is dagger's internal interface to buildkit APIs
type Client struct {
	Opts
	session   *bksession.Session
	job       *bksolver.Job
	llbBridge bkfrontend.FrontendLLBBridge

	clientHostnameToID   map[string]string
	clientHostnameToIDMu sync.RWMutex

	refs         map[*ref]struct{}
	refsMu       sync.Mutex
	containers   map[bkgw.Container]struct{}
	containersMu sync.Mutex
}

type Result = solverresult.Result[*ref]

func NewClient(ctx context.Context, opts Opts) (*Client, error) {
	client := &Client{
		Opts:               opts,
		clientHostnameToID: make(map[string]string),
		refs:               make(map[*ref]struct{}),
		containers:         make(map[bkgw.Container]struct{}),
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

func (c *Client) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
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
			ref, ok := m.Ref.(*ref)
			if !ok {
				return fmt.Errorf("unexpected ref type: %T", m.Ref)
			}
			var workerRef *bkworker.WorkerRef
			if ref != nil {
				res, err := ref.resultProxy.Result(egctx)
				if err != nil {
					return err
				}
				var ok bool
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
		return nil, err
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

func (c *Client) RegisterClient(clientID, clientHostname string) {
	c.clientHostnameToIDMu.Lock()
	defer c.clientHostnameToIDMu.Unlock()
	// TODO: error out if clientID already exists? How would a user accomplish that? They'd need to somehow connect to the same router id
	c.clientHostnameToID[clientHostname] = clientID
}

func (c *Client) DeregisterClientHostname(clientHostname string) {
	c.clientHostnameToIDMu.Lock()
	defer c.clientHostnameToIDMu.Unlock()
	delete(c.clientHostnameToID, clientHostname)
}

func (c *Client) getClientIDByHostname(clientHostname string) (string, error) {
	c.clientHostnameToIDMu.RLock()
	defer c.clientHostnameToIDMu.RUnlock()
	clientID, ok := c.clientHostnameToID[clientHostname]
	if !ok {
		// TODO:
		// return "", fmt.Errorf("client hostname %q not found", clientHostname)
		return "", fmt.Errorf("client hostname %q not found: %s", clientHostname, string(debug.Stack()))
	}
	return clientID, nil
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

	// TODO: double check that reading the client id from the context here is correct, and that it shouldn't
	// instead be deser'd from the local name. I think it's okay provided we still do the local dir import
	// synchronously in the caller of this.
	nameBytes, err := json.Marshal(localImportOpts{
		// For now, the requester is always the owner of the local dir
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

	_, descRef, err := expInstance.Export(ctx, cacheRes, c.ID())
	if err != nil {
		return fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return nil
}

type PublishInput struct {
	Definition *bksolverpb.Definition
	Config     specs.ImageConfig
}

func (c *Client) PublishContainerImage(
	ctx context.Context,
	inputByPlatform map[string]PublishInput,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(inputByPlatform)),
	}
	// TODO: probably faster to do this in parallel for each platform
	for platformString, input := range inputByPlatform {
		// TODO: add util for turning into cacheRes, dedupe w/ above
		res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: input.Definition})
		if err != nil {
			return nil, fmt.Errorf("failed to solve for container publish: %s", err)
		}
		cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
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
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %s", err)
		}
		ref, err := cacheRes.SingleRef()
		if err != nil {
			return nil, err
		}

		platform, err := platforms.Parse(platformString)
		if err != nil {
			return nil, err
		}
		cfgBytes, err := json.Marshal(specs.Image{
			Platform: specs.Platform{
				Architecture: platform.Architecture,
				OS:           platform.OS,
				OSVersion:    platform.OSVersion,
				OSFeatures:   platform.OSFeatures,
			},
			Config: input.Config,
		})
		if err != nil {
			return nil, err
		}
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
			expPlatforms.Platforms[len(combinedResult.Refs)] = exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			}
			combinedResult.AddRef(platformString, ref)
		}
	}
	if len(combinedResult.Refs) > 1 {
		platformBytes, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)
	}

	exporter, err := c.Worker.Exporter(bkclient.ExporterImage, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, c.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

// TODO: dedupe w/ above
func (c *Client) ExportContainerImage(
	ctx context.Context,
	inputByPlatform map[string]PublishInput, //TODO: publish input as a name makes no sense anymore
	destPath string,
	opts map[string]string, // TODO: make this an actual type, this leaks too much untyped buildkit api
) (map[string]string, error) {
	combinedResult := &solverresult.Result[bkcache.ImmutableRef]{}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(inputByPlatform)),
	}
	// TODO: probably faster to do this in parallel for each platform
	for platformString, input := range inputByPlatform {
		// TODO: add util for turning into cacheRes, dedupe w/ above
		res, err := c.Solve(ctx, bkgw.SolveRequest{Definition: input.Definition})
		if err != nil {
			return nil, fmt.Errorf("failed to solve for container publish: %s", err)
		}
		cacheRes, err := solverresult.ConvertResult(res, func(rf *ref) (bkcache.ImmutableRef, error) {
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
		if err != nil {
			return nil, fmt.Errorf("failed to convert result: %s", err)
		}
		ref, err := cacheRes.SingleRef()
		if err != nil {
			return nil, err
		}

		platform, err := platforms.Parse(platformString)
		if err != nil {
			return nil, err
		}
		cfgBytes, err := json.Marshal(specs.Image{
			Platform: specs.Platform{
				Architecture: platform.Architecture,
				OS:           platform.OS,
				OSVersion:    platform.OSVersion,
				OSFeatures:   platform.OSFeatures,
			},
			Config: input.Config,
		})
		if err != nil {
			return nil, err
		}
		if len(inputByPlatform) == 1 {
			combinedResult.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
			combinedResult.SetRef(ref)
		} else {
			combinedResult.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformString), cfgBytes)
			expPlatforms.Platforms[len(combinedResult.Refs)] = exptypes.Platform{
				ID:       platformString,
				Platform: platform,
			}
			combinedResult.AddRef(platformString, ref)
		}
	}
	if len(combinedResult.Refs) > 1 {
		platformBytes, err := json.Marshal(expPlatforms)
		if err != nil {
			return nil, err
		}
		combinedResult.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)
	}

	exporterName := bkclient.ExporterDocker
	if len(combinedResult.Refs) > 1 {
		exporterName = bkclient.ExporterOCI
	}

	exporter, err := c.Worker.Exporter(exporterName, c.SessionManager)
	if err != nil {
		return nil, err
	}

	expInstance, err := exporter.Resolve(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve exporter: %s", err)
	}

	// TODO: workaround needed until upstream fix: https://github.com/moby/buildkit/pull/4049
	// TODO: This probably doesn't entirely work yet in the case where the combined result is still
	// lazy and relies on other session resources to be evaluated. Fix that if merging before upstream
	// fix in place.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sess, err := c.newFileSendServerProxySession(ctx, destPath)
	if err != nil {
		return nil, err
	}

	resp, descRef, err := expInstance.Export(ctx, combinedResult, sess.ID())
	if err != nil {
		return nil, fmt.Errorf("failed to export: %s", err)
	}
	if descRef != nil {
		descRef.Release()
	}
	return resp, nil
}

type hostSocketOpts struct {
	HostPath       string `json:"host_path,omitempty"`
	ClientHostname string `json:"client_hostname,omitempty"`
}

func (c *Client) SocketLLBID(hostPath, clientHostname string) (string, error) {
	idBytes, err := json.Marshal(hostSocketOpts{
		HostPath:       hostPath,
		ClientHostname: clientHostname,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(idBytes), nil
}

func withOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}
