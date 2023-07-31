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
	"github.com/dagger/dagger/engine/buildkit"
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
	"google.golang.org/grpc/metadata"
)

const OCIStoreName = "dagger-oci"

type ClientParams struct {
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
	ClientParams
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

func Connect(ctx context.Context, params ClientParams) (_ *Client, _ context.Context, rerr error) {
	s := &Client{ClientParams: params}
	if s.SecretToken == "" {
		s.SecretToken = uuid.New().String()
	}
	if s.ServerID == "" {
		s.ServerID = identity.NewID()
	}

	s.internalCtx, s.internalCancel = context.WithCancel(context.Background())
	s.eg, s.internalCtx = errgroup.WithContext(s.internalCtx)
	defer func() {
		if rerr != nil {
			s.internalCancel()
		}
	}()
	s.closeCtx, s.closeRequests = context.WithCancel(context.Background())

	// progress
	progMultiW := progrock.MultiWriter{}
	if s.ProgrockWriter != nil {
		progMultiW = append(progMultiW, s.ProgrockWriter)
	}
	if s.JournalFile != "" {
		fw, err := newProgrockFileWriter(s.JournalFile)
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
	if s.CloudURLCallback != nil && cloudURL != "" {
		s.CloudURLCallback(cloudURL)
	}

	nestedSessionPortVal, isNestedSession := os.LookupEnv("DAGGER_SESSION_PORT")
	if isNestedSession {
		nestedSessionPort, err := strconv.Atoi(nestedSessionPortVal)
		if err != nil {
			return nil, nil, fmt.Errorf("parse DAGGER_SESSION_PORT: %w", err)
		}
		s.nestedSessionPort = nestedSessionPort
		s.SecretToken = os.Getenv("DAGGER_SESSION_TOKEN")
		s.httpClient = &http.Client{
			Transport: &http.Transport{
				DialContext:       s.NestedDialContext,
				DisableKeepAlives: true,
			},
		}

		recorder := progrock.NewRecorder(progMultiW)
		s.Recorder = recorder
		ctx = progrock.RecorderToContext(ctx, s.Recorder)
		return s, ctx, nil
	}

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	cacheConfigType, cacheConfigAttrs, err := cacheConfigFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("cache config from env: %w", err)
	}
	if cacheConfigType != "" {
		s.upstreamCacheOptions = []*controlapi.CacheOptionsEntry{{
			Type:  cacheConfigType,
			Attrs: cacheConfigAttrs,
		}}
	}

	remote, err := url.Parse(s.RunnerHost)
	if err != nil {
		return nil, nil, fmt.Errorf("parse runner host: %w", err)
	}

	bkClient, err := newBuildkitClient(ctx, remote, s.UserAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("new client: %w", err)
	}
	s.bkClient = bkClient
	defer func() {
		if rerr != nil {
			s.bkClient.Close()
		}
	}()
	if s.EngineNameCallback != nil {
		info, err := s.bkClient.Info(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("get info: %w", err)
		}
		engineName := fmt.Sprintf("%s (version %s)", info.BuildkitVersion.Package, info.BuildkitVersion.Version)
		s.EngineNameCallback(engineName)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, nil, fmt.Errorf("get hostname: %w", err)
	}
	s.hostname = hostname

	sharedKey := s.ServerID // share a session across servers
	bkSession, err := bksession.NewSession(ctx, identity.NewID(), sharedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("new s: %w", err)
	}
	s.bkSession = bkSession
	defer func() {
		if rerr != nil {
			s.bkSession.Close()
		}
	}()

	workdir, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("get workdir: %w", err)
	}
	s.internalCtx = engine.ContextWithClientMetadata(s.internalCtx, &engine.ClientMetadata{
		ClientID:          s.ID(),
		ClientSecretToken: s.SecretToken,
		ServerID:          s.ServerID,
		ClientHostname:    s.hostname,
		Labels:            pipeline.LoadVCSLabels(workdir),
		ParentClientIDs:   s.ParentClientIDs,
	})

	// progress
	bkSession.Allow(progRockAttachable{progMultiW})

	// filesync
	if !s.DisableHostRW {
		bkSession.Allow(AnyDirSource{})
		bkSession.Allow(AnyDirTarget{})
	}

	// sockets
	bkSession.Allow(SocketProvider{
		EnableHostNetworkAccess: !s.DisableHostRW,
	})

	// registry auth
	bkSession.Allow(authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)))

	// connect to the server, registering our session attachables and starting the server if not
	// already started
	s.eg.Go(func() error {
		return bkSession.Run(s.internalCtx, func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			return grpchijack.Dialer(s.bkClient.ControlClient())(ctx, proto, engine.ClientMetadata{
				RegisterClient:      true,
				ClientID:            s.ID(),
				ClientSecretToken:   s.SecretToken,
				ServerID:            s.ServerID,
				ParentClientIDs:     s.ParentClientIDs,
				ClientHostname:      hostname,
				UpstreamCacheConfig: s.upstreamCacheOptions,
			}.AppendToMD(meta))
		})
	})

	// Try connecting to the session server to make sure it's running
	s.httpClient = &http.Client{Transport: &http.Transport{
		DialContext: s.DialContext,
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
		return s.Do(ctx, `{defaultPlatform}`, "", nil, nil)
	}, backoff.WithContext(bo, connectRetryCtx))
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}

	return s, ctx, nil
}

func (s *Client) Close() (rerr error) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	select {
	case <-s.closeCtx.Done():
		// already closed
		return nil
	default:
	}
	if len(s.upstreamCacheOptions) > 0 {
		cacheExportCtx, cacheExportCancel := context.WithTimeout(s.internalCtx, 600*time.Second)
		defer cacheExportCancel()
		_, err := s.bkClient.ControlClient().Solve(cacheExportCtx, &controlapi.SolveRequest{
			Cache: controlapi.CacheOptions{
				Exports: s.upstreamCacheOptions,
			},
		})
		rerr = errors.Join(rerr, err)
	}

	s.closeRequests()

	if s.internalCancel != nil {
		s.internalCancel()
	}

	if s.httpClient != nil {
		s.eg.Go(func() error {
			s.httpClient.CloseIdleConnections()
			return nil
		})
	}

	if s.bkSession != nil {
		s.eg.Go(s.bkSession.Close)
	}
	if s.bkClient != nil {
		s.eg.Go(s.bkClient.Close)
	}
	if err := s.eg.Wait(); err != nil {
		rerr = errors.Join(rerr, err)
	}

	// mark all groups completed
	// close the recorder so the UI exits
	if s.Recorder != nil {
		s.Recorder.Complete()
		s.Recorder.Close()
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

func (s *Client) ID() string {
	return s.bkSession.ID()
}

func (s *Client) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	// NOTE: the context given to grpchijack.Dialer is for the lifetime of the stream.
	// If http connection re-use is enabled, that can be far past this DialContext call.
	ctx, cancel, err := s.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := grpchijack.Dialer(s.bkClient.ControlClient())(ctx, "", engine.ClientMetadata{
		ClientID:          s.ID(),
		ClientSecretToken: s.SecretToken,
		ServerID:          s.ServerID,
		ClientHostname:    s.hostname,
		ParentClientIDs:   s.ParentClientIDs,
	}.ToGRPCMD())
	if err != nil {
		return nil, err
	}
	go func() {
		<-s.closeCtx.Done()
		cancel()
		conn.Close()
	}()
	return conn, nil
}

func (s *Client) NestedDialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	ctx, cancel, err := s.withClientCloseCancel(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := (&net.Dialer{
		Cancel:    ctx.Done(),
		KeepAlive: -1, // disable for now
	}).Dial("tcp", "127.0.0.1:"+strconv.Itoa(s.nestedSessionPort))
	if err != nil {
		return nil, err
	}
	go func() {
		<-s.closeCtx.Done()
		cancel()
		conn.Close()
	}()
	return conn, nil
}

func (s *Client) Do(
	ctx context.Context,
	query string,
	opName string,
	variables map[string]any,
	data any,
) (rerr error) {
	ctx, cancel, err := s.withClientCloseCancel(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	gqlClient := graphql.NewClient("http://dagger/query", doerWithHeaders{
		inner: s.httpClient,
		headers: http.Header{
			"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(s.SecretToken+":"))},
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

func (s *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, err := s.withClientCloseCancel(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("client has closed: " + err.Error()))
		return
	}
	r = r.WithContext(ctx)
	defer cancel()

	if s.SecretToken != "" {
		username, _, ok := r.BasicAuth()
		if !ok || username != s.SecretToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	resp, err := s.httpClient.Do(&http.Request{
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
	md, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	readSingleFileVals, ok := md[engine.LocalDirImportReadSingleFileMetaKey]
	if ok {
		if len(readSingleFileVals) != 1 {
			return fmt.Errorf("expected exactly one %s, got %d", engine.LocalDirImportReadSingleFileMetaKey, len(readSingleFileVals))
		}
		filePath := readSingleFileVals[0]
		fileContents, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		if len(fileContents) > buildkit.MaxFileContentsChunkSize {
			// NOTE: can lift this size restriction by chunking if ever needed
			return fmt.Errorf("file contents too large: %d > %d", len(fileContents), buildkit.MaxFileContentsChunkSize)
		}
		return stream.SendMsg(&filesync.BytesMessage{Data: fileContents})
	}

	dirNameVals := md[engine.LocalDirImportDirNameMetaKey]
	if len(dirNameVals) != 1 {
		return fmt.Errorf("expected exactly one %s, got %d", engine.LocalDirImportDirNameMetaKey, len(dirNameVals))
	}
	dirName := dirNameVals[0]
	includePatterns := md[engine.LocalDirImportIncludePatternsMetaKey]
	excludePatterns := md[engine.LocalDirImportExcludePatternsMetaKey]
	followPaths := md[engine.LocalDirImportFollowPathsMetaKey]
	return fsutil.Send(stream.Context(), stream, fsutil.NewFS(dirName, &fsutil.WalkOpt{
		IncludePatterns: includePatterns,
		ExcludePatterns: excludePatterns,
		FollowPaths:     followPaths,
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
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return fmt.Errorf("diff copy missing metadata")
	}

	destVal, ok := opts[engine.LocalDirExportDestPathMetaKey]
	if !ok {
		return fmt.Errorf("missing " + engine.LocalDirExportDestPathMetaKey)
	}
	if len(destVal) != 1 {
		return fmt.Errorf("expected exactly one "+engine.LocalDirExportDestPathMetaKey+" value, got %d", len(destVal))
	}
	dest := destVal[0]

	_, isFileStream := opts[engine.LocalDirExportIsFileStreamMetaKey]

	if !isFileStream {
		// we're writing a full directory tree, normal fsutil.Receive is good
		if err := os.MkdirAll(dest, 0700); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", dest, err)
		}

		err := fsutil.Receive(stream.Context(), stream, dest, fsutil.ReceiveOpt{
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
	_, allowParentDirPath := opts[engine.LocalDirExportAllowParentDirPathMetaKey]

	// File exports specifically (as opposed to container tar exports) have an original filename that we will
	// use in the case where dest is a directory and allowParentDirPath is set, in which case we need to know
	// what to name the file underneath the pre-existing directory.
	var fileOriginalName string
	fileOriginalNameVal, hasFileOriginalName := opts[engine.LocalDirExportFileOriginalNameMetaKey]
	if hasFileOriginalName {
		if len(fileOriginalNameVal) != 1 {
			return fmt.Errorf("expected exactly one "+engine.LocalDirExportFileOriginalNameMetaKey+" value, got %d", len(fileOriginalNameVal))
		}
		fileOriginalName = filepath.Join("/", fileOriginalNameVal[0])
	}

	var destParentDir string
	var finalDestPath string
	stat, err := os.Lstat(dest)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// we are writing the file to a new path
		destParentDir = filepath.Dir(dest)
		finalDestPath = dest
	case err != nil:
		// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
		return fmt.Errorf("failed to stat synctarget dest %s: %w", dest, err)
	case !stat.IsDir():
		// we are overwriting an existing file
		destParentDir = filepath.Dir(dest)
		finalDestPath = dest
	case !allowParentDirPath:
		// we are writing to an existing directory, but allowParentDirPath is not set, so fail
		return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", dest)
	default:
		// we are writing to an existing directory, and allowParentDirPath is set,
		// so write the file under the directory using the same file name as the source file
		if !hasFileOriginalName {
			// NOTE: we could instead just default to some name like container.tar or something if desired
			return fmt.Errorf("cannot export container tar to existing directory %q", dest)
		}
		destParentDir = dest
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
