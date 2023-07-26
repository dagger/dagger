package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/cenkalti/backoff/v4"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/telemetry"
	"github.com/docker/cli/cli/config"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const OCIStoreName = "dagger-oci"

type SessionParams struct {
	// The id of the router to connect to, or if blank a new one
	// should be started.
	RouterID string

	// Parent client IDs of this Dagger client.
	//
	// Used by Dagger-in-Dagger so that nested sessions can resolve addresses
	// passed from the parent.
	ParentClientIDs []string

	// TODO: re-add support
	SecretToken string

	RunnerHost string // host of dagger engine runner serving buildkit apis
	UserAgent  string

	DisableHostRW bool

	JournalFile        string
	ProgrockWriter     progrock.Writer
	EngineNameCallback func(string)
	CloudURLCallback   func(string)
}

// TODO: probably rename Session to something like Client
type Session struct {
	SessionParams
	eg             *errgroup.Group
	internalCancel context.CancelFunc

	Recorder *progrock.Recorder

	httpClient           *http.Client
	bkClient             *bkclient.Client
	bkSession            *bksession.Session
	upstreamCacheOptions []*controlapi.CacheOptionsEntry

	hostname string
}

func Connect(ctx context.Context, params SessionParams) (_ *Session, rerr error) {
	s := &Session{SessionParams: params}

	if s.RouterID == "" {
		s.RouterID = identity.NewID()
	}

	// TODO: this is only needed temporarily to work around issue w/
	// `dagger do` and `dagger project` not picking up env set by nesting
	// (which impacts project tests). Remove ASAP
	if v, ok := os.LookupEnv("_DAGGER_ROUTER_ID"); ok {
		s.RouterID = v
	}

	internalCtx, internalCancel := context.WithCancel(context.Background())
	defer func() {
		if rerr != nil {
			internalCancel()
		}
	}()
	s.internalCancel = internalCancel
	s.eg, internalCtx = errgroup.WithContext(internalCtx)

	// Check if any of the upstream cache importers/exporters are enabled.
	// Note that this is not the cache service support in engine/cache/, that
	// is a different feature which is configured in the engine daemon.
	cacheConfigType, cacheConfigAttrs, err := cacheConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("cache config from env: %w", err)
	}
	if cacheConfigType != "" {
		s.upstreamCacheOptions = []*controlapi.CacheOptionsEntry{{
			Type:  cacheConfigType,
			Attrs: cacheConfigAttrs,
		}}
	}

	remote, err := url.Parse(s.RunnerHost)
	if err != nil {
		return nil, fmt.Errorf("parse runner host: %w", err)
	}

	bkClient, err := newBuildkitClient(ctx, remote, s.UserAgent)
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}
	s.bkClient = bkClient
	defer func() {
		if rerr != nil {
			s.bkClient.Close()
		}
	}()

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("get hostname: %w", err)
	}
	s.hostname = hostname
	bkSessionName := s.hostname
	sharedKey := s.RouterID

	bkSession, err := bksession.NewSession(ctx, bkSessionName, sharedKey)
	if err != nil {
		return nil, fmt.Errorf("new s: %w", err)
	}
	s.bkSession = bkSession
	defer func() {
		if rerr != nil {
			s.bkSession.Close()
		}
	}()

	workdir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get workdir: %w", err)
	}
	internalCtx = engine.ContextWithClientMetadata(internalCtx, &engine.ClientMetadata{
		ClientID:        s.ID(),
		RouterID:        s.RouterID,
		ClientHostname:  s.hostname,
		ParentClientIDs: s.ParentClientIDs,
		Labels:          pipeline.LoadVCSLabels(workdir),
	})

	// filesync
	if !s.DisableHostRW {
		bkSession.Allow(filesync.NewFSSyncProvider(AnyDirSource{}))
		bkSession.Allow(AnyDirTarget{})
	}

	// sockets
	bkSession.Allow(SocketProvider{
		EnableHostNetworkAccess: !s.DisableHostRW,
	})

	// registry auth
	bkSession.Allow(authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr)))

	// progress
	progMultiW := progrock.MultiWriter{}
	if s.ProgrockWriter != nil {
		progMultiW = append(progMultiW, s.ProgrockWriter)
	}
	if s.JournalFile != "" {
		fw, err := newProgrockFileWriter(s.JournalFile)
		if err != nil {
			return nil, err
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

	if s.EngineNameCallback != nil {
		info, err := s.bkClient.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("get info: %w", err)
		}
		engineName := fmt.Sprintf("%s (version %s)", info.BuildkitVersion.Package, info.BuildkitVersion.Version)
		s.EngineNameCallback(engineName)
	}

	bkSession.Allow(progRockAttachable{progMultiW})
	recorder := progrock.NewRecorder(progMultiW)
	s.Recorder = recorder

	// start the router if it's not already running, client+router ID are
	// passed through grpc context
	s.eg.Go(func() error {
		_, err := s.bkClient.ControlClient().Solve(internalCtx, &controlapi.SolveRequest{
			Cache: controlapi.CacheOptions{
				// Exports are handled in Close
				Imports: s.upstreamCacheOptions,
			},
		})
		return err
	})

	// run the client session
	s.eg.Go(func() error {
		// client ID and router ID get passed via the session ID and session shared key respectively,
		// this is because in order to explicitly set those in our own code we'd need to copy
		// a lot of internal code; could be addressed with upstream tweaks to make some types
		// public
		return bkSession.Run(internalCtx, func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			meta[engine.RouterIDMetaKey] = []string{s.RouterID}
			log.Println("!!! SETTING PARENT CLIENT IDS", s.RouterID, s.ParentClientIDs)
			meta[engine.ParentClientIDsMetaKey] = s.ParentClientIDs
			meta[engine.ClientHostnameMetaKey] = []string{hostname}
			return grpchijack.Dialer(s.bkClient.ControlClient())(ctx, proto, meta)
		})
	})

	// Try connecting to the session server to make sure it's running
	s.httpClient = &http.Client{Transport: &http.Transport{DialContext: s.DialContext}}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	err = backoff.Retry(func() error {
		ctx, cancel := context.WithTimeout(ctx, bo.NextBackOff())
		defer cancel()
		return s.Do(ctx, `{defaultPlatform}`, "", nil, nil)
	}, backoff.WithContext(bo, ctx)) // TODO: this stops if context is canceled right?
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	return s, nil
}

func (s *Session) Close() (rerr error) {
	if len(s.upstreamCacheOptions) > 0 {
		// TODO: cancelable context here?
		ctx := engine.ContextWithClientMetadata(context.TODO(), &engine.ClientMetadata{
			ClientID:       s.ID(),
			RouterID:       s.RouterID,
			ClientHostname: s.hostname,
		})
		_, err := s.bkClient.ControlClient().Solve(ctx, &controlapi.SolveRequest{
			Cache: controlapi.CacheOptions{
				Exports: s.upstreamCacheOptions,
			},
		})
		// TODO:
		// TODO:
		// TODO:
		if err != nil {
			fmt.Fprintf(os.Stderr, "cache export err: %v\n", err)
		}
		rerr = errors.Join(rerr, err)
	}

	// mark all groups completed
	// close the recorder so the UI exits
	// TODO: should this be done after session confirmed close instead?
	s.Recorder.Complete()
	s.Recorder.Close()

	if s.internalCancel != nil {
		s.internalCancel()
		s.bkSession.Close()
		s.bkClient.Close()
		if err := s.eg.Wait(); err != nil {
			rerr = errors.Join(rerr, err)
		}
	}
	return
}

func (s *Session) ID() string {
	return s.bkSession.ID()
}

func (s *Session) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return grpchijack.Dialer(s.bkClient.ControlClient())(ctx, "", engine.ClientMetadata{
		ClientID:        s.ID(),
		RouterID:        s.RouterID,
		ClientHostname:  s.hostname,
		ParentClientIDs: s.ParentClientIDs,
	}.ToGRPCMD())
}

func (s *Session) Do(
	ctx context.Context,
	query string,
	opName string,
	variables map[string]any,
	data any,
) error {
	gqlClient := graphql.NewClient("http://dagger/query", s.httpClient)

	req := &graphql.Request{
		Query:     query,
		Variables: variables,
		OpName:    opName,
	}
	resp := &graphql.Response{}

	err := gqlClient.MakeRequest(ctx, req, resp)
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

func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO: thought httputil.ReverseProxy would do this, but got weird errors and gave up, try again?
	newReq := &http.Request{
		Method: r.Method,
		URL: &url.URL{
			Scheme: "http",
			Host:   "dagger",
			Path:   r.URL.Path,
		},
		Header: r.Header,
		Body:   r.Body,
		// TODO:
		// Body: io.NopCloser(io.TeeReader(r.Body, os.Stderr)),
	}
	resp, err := s.httpClient.Do(newReq)
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

func (AnyDirSource) LookupDir(name string) (filesync.SyncedDir, bool) {
	return filesync.SyncedDir{
		Dir: name,
		Map: func(p string, st *fstypes.Stat) fsutil.MapResult {
			st.Uid = 0
			st.Gid = 0
			return fsutil.MapResultKeep
		},
	}, true
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

	_, isWriteStream := opts[engine.LocalDirExportWriteStreamMetaKey]

	if isWriteStream {
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", dest, err)
		}
		// TODO: set specific permissions?
		destF, err := os.Create(dest)
		if err != nil {
			return fmt.Errorf("failed to create synctarget dest file %s: %w", dest, err)
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

	_, allowParentDirPath := opts[engine.LocalDirExportAllowParentDirPathMetaKey]
	fileSourcePathVal, isFileExport := opts[engine.LocalDirExportFileSourcePathMetaKey]
	if isFileExport {
		if len(fileSourcePathVal) != 1 {
			return fmt.Errorf("expected exactly one "+engine.LocalDirExportFileSourcePathMetaKey+" value, got %d", len(fileSourcePathVal))
		}
		fileSourcePath := filepath.Join("/", fileSourcePathVal[0]) // TODO: double check joining with / is correct

		var destParentDir string
		var syncedDestPath string
		var finalDestPath string
		// TODO: lstat correct? what happened with symlinks before? just maintain previous behavior (and add test coverage)
		stat, err := os.Lstat(dest)
		switch {
		case errors.Is(err, os.ErrNotExist):
			// we are writing the file to a new path
			destParentDir = filepath.Dir(dest)
			syncedDestPath = filepath.Join(destParentDir, filepath.Base(fileSourcePath))
			finalDestPath = dest
		case err != nil:
			// something went unrecoverably wrong if stat failed and it wasn't just because the path didn't exist
			return fmt.Errorf("failed to stat synctarget dest %s: %w", dest, err)
		case !stat.IsDir():
			// we are overwriting an existing file
			destParentDir = filepath.Dir(dest)
			syncedDestPath = filepath.Join(destParentDir, filepath.Base(fileSourcePath))
			finalDestPath = dest
		case !allowParentDirPath:
			// we are writing to an existing directory, but allowParentDirPath is not set, so fail
			return fmt.Errorf("destination %q is a directory; must be a file path unless allowParentDirPath is set", dest)
		default:
			// we are writing to an existing directory, and allowParentDirPath is set,
			// so write the file under the directory using the same file name as the source file
			destParentDir = dest
			syncedDestPath = filepath.Join(destParentDir, filepath.Base(fileSourcePath))
			finalDestPath = syncedDestPath
		}
		if destParentDir == "" {
			return fmt.Errorf("failed to determine parent dir of synctarget dest %s", dest)
		}
		if syncedDestPath == "" {
			return fmt.Errorf("failed to determine synced dest path of synctarget dest %s", dest)
		}
		if finalDestPath == "" {
			return fmt.Errorf("failed to determine final dest path of synctarget dest %s", dest)
		}

		if err := os.MkdirAll(destParentDir, 0700); err != nil {
			return fmt.Errorf("failed to create synctarget dest dir %s: %w", destParentDir, err)
		}

		err = fsutil.Receive(stream.Context(), stream, destParentDir, fsutil.ReceiveOpt{
			Merge: true,
			Filter: func(path string, stat *fstypes.Stat) bool {
				path = filepath.Join("/", path)
				if path != fileSourcePath {
					return false
				}
				stat.Uid = uint32(os.Getuid())
				stat.Gid = uint32(os.Getgid())
				return true
			},
		})
		if status.Code(err) == codes.Canceled {
			// TODO: is there a way to be sure that the stream was actually done? as opposed to just canceled?
			err = nil
		}
		if err != nil {
			return fmt.Errorf("failed to receive fs changes: %w", err)
		}
		if syncedDestPath != finalDestPath {
			if err := os.Rename(syncedDestPath, finalDestPath); err != nil {
				return fmt.Errorf("failed to rename synced dest path %s to final dest path %s: %w", syncedDestPath, finalDestPath, err)
			}
		}
		return nil
	}

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
