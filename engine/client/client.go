package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/cenkalti/backoff/v4"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/telemetry"
	"github.com/docker/cli/cli/config"
	"github.com/google/uuid"
	controlapi "github.com/moby/buildkit/api/services/control"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/grpchijack"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

const OCIStoreName = "dagger-oci"

type Params struct {
	// The id of the server to connect to, or if blank a new one
	// should be started.
	ServerID string

	// Parent client IDs of this Dagger client.
	//
	// Used by Dagger-in-Dagger so that nested sessions can resolve addresses
	// passed from the parent.
	ParentClientIDs []string

	SecretToken string

	RunnerHost string // host of dagger engine runner serving buildkit apis
	UserAgent  string

	DisableHostRW bool

	JournalFile        string
	ProgrockWriter     progrock.Writer
	EngineNameCallback func(string)
	CloudURLCallback   func(string)
}

type Client struct {
	Params
	eg             *errgroup.Group
	internalCtx    context.Context
	internalCancel context.CancelFunc

	closeCtx      context.Context
	closeRequests context.CancelFunc
	closeMu       sync.RWMutex

	Recorder *progrock.Recorder

	httpClient           *http.Client
	bkClient             *bkclient.Client
	bkSession            *bksession.Session
	upstreamCacheOptions []*controlapi.CacheOptionsEntry

	hostname string

	nestedSessionPort int
}

func Connect(ctx context.Context, params Params) (_ *Client, _ context.Context, rerr error) {
	c := &Client{Params: params}
	if c.SecretToken == "" {
		c.SecretToken = uuid.New().String()
	}
	if c.ServerID == "" {
		c.ServerID = identity.NewID()
	}

	c.internalCtx, c.internalCancel = context.WithCancel(context.Background())
	c.eg, c.internalCtx = errgroup.WithContext(c.internalCtx)
	defer func() {
		if rerr != nil {
			c.internalCancel()
		}
	}()
	c.closeCtx, c.closeRequests = context.WithCancel(context.Background())

	// progress
	progMultiW := progrock.MultiWriter{}
	if c.ProgrockWriter != nil {
		progMultiW = append(progMultiW, c.ProgrockWriter)
	}
	if c.JournalFile != "" {
		fw, err := newProgrockFileWriter(c.JournalFile)
		if err != nil {
			return nil, nil, err
		}

		progMultiW = append(progMultiW, fw)
	}

	tel := telemetry.New()
	var cloudURL string
	if tel.Enabled() {
		cloudURL = tel.URL()
		progMultiW = append(progMultiW, telemetry.NewWriter(tel))
	}
	// NB(vito): use a _passthrough_ recorder at this layer, since we don't want
	// to initialize a group; that's handled by the other side.
	//
	// TODO: this is pretty confusing and could probably be refactored. afaict
	// it's split up this way because we need the engine name to be part of the
	// root group labels, but maybe that could be handled differently.
	recorder := progrock.NewPassthroughRecorder(progMultiW)
	c.Recorder = recorder
	ctx = progrock.RecorderToContext(ctx, c.Recorder)

	nestedSessionPortVal, isNestedSession := os.LookupEnv("DAGGER_SESSION_PORT")
	if isNestedSession {
		nestedSessionPort, err := strconv.Atoi(nestedSessionPortVal)
		if err != nil {
			return nil, nil, fmt.Errorf("parse DAGGER_SESSION_PORT: %w", err)
		}
		c.nestedSessionPort = nestedSessionPort
		c.SecretToken = os.Getenv("DAGGER_SESSION_TOKEN")
		c.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext:       c.NestedDialContext,
				DisableKeepAlives: true,
			},
		}
		return c, ctx, nil
	}

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	cacheConfigType, cacheConfigAttrs, err := cacheConfigFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("cache config from env: %w", err)
	}
	if cacheConfigType != "" {
		c.upstreamCacheOptions = []*controlapi.CacheOptionsEntry{{
			Type:  cacheConfigType,
			Attrs: cacheConfigAttrs,
		}}
	}

	remote, err := url.Parse(c.RunnerHost)
	if err != nil {
		return nil, nil, fmt.Errorf("parse runner host: %w", err)
	}

	bkClient, err := newBuildkitClient(ctx, remote, c.UserAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("new client: %w", err)
	}
	c.bkClient = bkClient
	defer func() {
		if rerr != nil {
			c.bkClient.Close()
		}
	}()
	if c.EngineNameCallback != nil {
		info, err := c.bkClient.Info(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("get info: %w", err)
		}
		engineName := fmt.Sprintf("%s (version %s)", info.BuildkitVersion.Package, info.BuildkitVersion.Version)
		c.EngineNameCallback(engineName)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, nil, fmt.Errorf("get hostname: %w", err)
	}
	c.hostname = hostname

	sharedKey := c.ServerID // share a session across servers
	bkSession, err := bksession.NewSession(ctx, identity.NewID(), sharedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("new s: %w", err)
	}
	c.bkSession = bkSession
	defer func() {
		if rerr != nil {
			c.bkSession.Close()
		}
	}()

	workdir, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get workdir: %w", err)
	}
	c.internalCtx = engine.ContextWithClientMetadata(c.internalCtx, &engine.ClientMetadata{
		ClientID:          c.ID(),
		ClientSecretToken: c.SecretToken,
		ServerID:          c.ServerID,
		ClientHostname:    c.hostname,
		Labels:            pipeline.LoadVCSLabels(workdir),
		ParentClientIDs:   c.ParentClientIDs,
	})

	// progress
	bkSession.Allow(progRockAttachable{progMultiW})

	// filesync
	if !c.DisableHostRW {
		bkSession.Allow(AnyDirSource{})
		bkSession.Allow(AnyDirTarget{})
	}

	// sockets
	bkSession.Allow(SocketProvider{
		EnableHostNetworkAccess: !c.DisableHostRW,
	})

	// registry auth
	bkSession.Allow(authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)))

	// connect to the server, registering our session attachables and starting the server if not
	// already started
	c.eg.Go(func() error {
		return bkSession.Run(c.internalCtx, func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return grpchijack.Dialer(c.bkClient.ControlClient())(ctx, proto, engine.ClientMetadata{
				RegisterClient:      true,
				ClientID:            c.ID(),
				ClientSecretToken:   c.SecretToken,
				ServerID:            c.ServerID,
				ParentClientIDs:     c.ParentClientIDs,
				ClientHostname:      hostname,
				UpstreamCacheConfig: c.upstreamCacheOptions,
			}.AppendToMD(meta))
		})
	})

	// Try connecting to the session server to make sure it's running
	c.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: c.DialContext,
		// connection re-use in combination with the underlying grpc stream makes
		// managing the lifetime of connections very confusing, so disabling for now
		// TODO: For performance, it would be better to figure out a way to re-enable this
		DisableKeepAlives: true,
	}}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	connectRetryCtx, connectRetryCancel := context.WithTimeout(ctx, 300*time.Second)
	defer connectRetryCancel()
	err = backoff.Retry(func() error {
		ctx, cancel := context.WithTimeout(connectRetryCtx, bo.NextBackOff())
		defer cancel()
		return c.Do(ctx, `{defaultPlatform}`, "", nil, nil)
	}, backoff.WithContext(bo, connectRetryCtx))
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}

	if c.CloudURLCallback != nil && cloudURL != "" {
		c.CloudURLCallback(cloudURL)
	}

	return c, ctx, nil
}

func (c *Client) Close() (rerr error) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	select {
	case <-c.closeCtx.Done():
		// already closed
		return nil
	default:
	}
	if len(c.upstreamCacheOptions) > 0 {
		cacheExportCtx, cacheExportCancel := context.WithTimeout(c.internalCtx, 600*time.Second)
		defer cacheExportCancel()
		_, err := c.bkClient.ControlClient().Solve(cacheExportCtx, &controlapi.SolveRequest{
			Cache: controlapi.CacheOptions{
				Exports: c.upstreamCacheOptions,
			},
		})
		rerr = errors.Join(rerr, err)
	}

	c.closeRequests()

	if c.internalCancel != nil {
		c.internalCancel()
	}

	if c.httpClient != nil {
		c.eg.Go(func() error {
			c.httpClient.CloseIdleConnections()
			return nil
		})
	}

	if c.bkSession != nil {
		c.eg.Go(c.bkSession.Close)
	}
	if c.bkClient != nil {
		c.eg.Go(c.bkClient.Close)
	}
	if err := c.eg.Wait(); err != nil {
		rerr = errors.Join(rerr, err)
	}

	// mark all groups completed
	// close the recorder so the UI exits
	if c.Recorder != nil {
		c.Recorder.Complete()
		c.Recorder.Close()
	}

	return rerr
}

func (c *Client) withClientCloseCancel(ctx context.Context) (context.Context, context.CancelFunc, error) {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()
	select {
	case <-c.closeCtx.Done():
		return nil, nil, errors.New("client closed")
	default:
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-c.closeCtx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, nil
}

func (c *Client) ID() string {
	return c.bkSession.ID()
}

func (c *Client) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	// NOTE: the context given to grpchijack.Dialer is for the lifetime of the stream.
	// If http connection re-use is enabled, that can be far past this DialContext call.
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := grpchijack.Dialer(c.bkClient.ControlClient())(ctx, "", engine.ClientMetadata{
		ClientID:          c.ID(),
		ClientSecretToken: c.SecretToken,
		ServerID:          c.ServerID,
		ClientHostname:    c.hostname,
		ParentClientIDs:   c.ParentClientIDs,
	}.ToGRPCMD())
	if err != nil {
		return nil, err
	}
	go func() {
		<-c.closeCtx.Done()
		cancel()
		conn.Close()
	}()
	return conn, nil
}

func (c *Client) NestedDialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := (&net.Dialer{
		Cancel:    ctx.Done(),
		KeepAlive: -1, // disable for now
	}).Dial("tcp", "127.0.0.1:"+strconv.Itoa(c.nestedSessionPort))
	if err != nil {
		return nil, err
	}
	go func() {
		<-c.closeCtx.Done()
		cancel()
		conn.Close()
	}()
	return conn, nil
}

func (c *Client) Do(
	ctx context.Context,
	query string,
	opName string,
	variables map[string]any,
	data any,
) (rerr error) {
	ctx, cancel, err := c.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	gqlClient := graphql.NewClient("http://dagger/query", doerWithHeaders{
		inner: c.httpClient,
		headers: http.Header{
			"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(c.SecretToken+":"))},
		},
	})

	req := &graphql.Request{
		Query:     query,
		Variables: variables,
		OpName:    opName,
	}
	resp := &graphql.Response{}

	err = gqlClient.MakeRequest(ctx, req, resp)
	if err != nil {
		return fmt.Errorf("make request: %w", err)
	}
	if resp.Errors != nil {
		errs := make([]error, len(resp.Errors))
		for i, err := range resp.Errors {
			errs[i] = err
		}
		return errors.Join(errs...)
	}

	if data != nil {
		dataBytes, err := json.Marshal(resp.Data)
		if err != nil {
			return fmt.Errorf("marshal data: %w", err)
		}
		err = json.Unmarshal(dataBytes, data)
		if err != nil {
			return fmt.Errorf("unmarshal data: %w", err)
		}
	}
	return nil
}

func (c *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, err := c.withClientCloseCancel(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("client has closed: " + err.Error()))
		return
	}
	r = r.WithContext(ctx)
	defer cancel()

	if c.SecretToken != "" {
		username, _, ok := r.BasicAuth()
		if !ok || username != c.SecretToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	resp, err := c.httpClient.Do(&http.Request{
		Method: r.Method,
		URL: &url.URL{
			Scheme: "http",
			Host:   "dagger",
			Path:   r.URL.Path,
		},
		Header: r.Header,
		Body:   r.Body,
	})
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("http do: " + err.Error()))
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		panic(err) // don't write header because we already wrote to the body, which isn't allowed
	}
}

// Local dir imports
type AnyDirSource struct{}

func (s AnyDirSource) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s AnyDirSource) TarStream(stream filesync.FileSync_TarStreamServer) error {
	return fmt.Errorf("tarstream not supported")
}

func (s AnyDirSource) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local import opts: %w", err)
	}

	if opts.ReadSingleFileOnly {
		// just stream the file bytes to the caller
		fileContents, err := os.ReadFile(opts.Path)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		if len(fileContents) > int(opts.MaxFileSize) {
			// NOTE: can lift this size restriction by chunking if ever needed
			return fmt.Errorf("file contents too large: %d > %d", len(fileContents), opts.MaxFileSize)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: fileContents})
	}

	// otherwise, do the whole directory sync back to the caller
	return fsutil.Send(stream.Context(), stream, fsutil.NewFS(opts.Path, &fsutil.WalkOpt{
		IncludePatterns: opts.IncludePatterns,
		ExcludePatterns: opts.ExcludePatterns,
		FollowPaths:     opts.FollowPaths,
		Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
			st.Uid = 0
			st.Gid = 0
			return fsutil.MapResultKeep
		},
	}), nil)
}

// Local dir exports
type AnyDirTarget struct{}

func (t AnyDirTarget) Register(server *grpc.Server) {
	filesync.RegisterFileSendServer(server, t)
}

func (AnyDirTarget) DiffCopy(stream filesync.FileSend_DiffCopyServer) (rerr error) {
	opts, err := engine.LocalExportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local export opts: %w", err)
	}

	if !opts.IsFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := os.MkdirAll(opts.Path, 0700); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", opts.Path, err)
		}

		err := fsutil.Receive(stream.Context(), stream, opts.Path, fsutil.ReceiveOpt{
			Merge: true,
			Filter: func(path string, stat *fstypes.Stat) bool {
				stat.Uid = uint32(os.Getuid())
				stat.Gid = uint32(os.Getgid())
				return true
			},
		})
		if err != nil {
			return fmt.Errorf("failed to receive fs changes: %w", err)
		}
		return nil
	}

	// This is either a file export or a container tarball export, we'll just be receiving BytesMessages with
	// the contents and can write them directly to the destination path.

	// If the dest is a directory that already exists, we will never delete it and replace it with the file.
	// However, if allowParentDirPath is set, we will write the file underneath that existing directory.
	// But if allowParentDirPath is not set, which is the default setting in our API right now, we will return
	// an error when path is a pre-existing directory.
	allowParentDirPath := opts.AllowParentDirPath

	// File exports specifically (as opposed to container tar exports) have an original filename that we will
	// use in the case where dest is a directory and allowParentDirPath is set, in which case we need to know
	// what to name the file underneath the pre-existing directory.
	fileOriginalName := opts.FileOriginalName

	var destParentDir string
	var finalDestPath string
	stat, err := os.Lstat(opts.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", opts.Path, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(opts.Path)
		finalDestPath = opts.Path
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", opts.Path)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if fileOriginalName == "" {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", opts.Path)
		}
		destParentDir = opts.Path
		finalDestPath = filepath.Join(destParentDir, fileOriginalName)
	}

	if err := os.MkdirAll(destParentDir, 0700); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", destParentDir, err)
	}
	destF, err := os.OpenFile(finalDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create synctarget dest file %s: %w", finalDestPath, err)
	}
	defer destF.Close()
	for {
		msg := filesync.BytesMessage{}
		if err := stream.RecvMsg(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := destF.Write(msg.Data); err != nil {
			return err
		}
	}
}

type progRockAttachable struct {
	writer progrock.Writer
}

func (a progRockAttachable) Register(srv *grpc.Server) {
	progrock.RegisterProgressServiceServer(srv, progrock.NewRPCReceiver(a.writer))
}

const (
	cacheConfigEnvName = "_EXPERIMENTAL_DAGGER_CACHE_CONFIG"
)

func cacheConfigFromEnv() (string, map[string]string, error) {
	envVal, ok := os.LookupEnv(cacheConfigEnvName)
	if !ok {
		return "", nil, nil
	}

	// env is in form k1=v1,k2=v2,...
	kvs := strings.Split(envVal, ",")
	if len(kvs) == 0 {
		return "", nil, nil
	}
	attrs := make(map[string]string)
	for _, kv := range kvs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return "", nil, fmt.Errorf("invalid form for cache config %q", kv)
		}
		attrs[parts[0]] = parts[1]
	}
	typeVal, ok := attrs["type"]
	if !ok {
		return "", nil, fmt.Errorf("missing type in cache config: %q", envVal)
	}
	delete(attrs, "type")
	return typeVal, attrs, nil
}

type doerWithHeaders struct {
	inner   graphql.Doer
	headers http.Header
}

func (d doerWithHeaders) Do(req *http.Request) (*http.Response, error) {
	for k, v := range d.headers {
		req.Header[k] = v
	}
	return d.inner.Do(req)
}
