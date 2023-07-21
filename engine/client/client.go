package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/cenkalti/backoff/v4"
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
	"google.golang.org/grpc/metadata"
)

const OCIStoreName = "dagger-oci"

type SessionParams struct {
	// The id of the router to connect to, or if blank a new one
	// should be started.
	RouterID string

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

// TODO: probably rename Session to something
type Session struct {
	SessionParams
	eg             *errgroup.Group
	internalCancel context.CancelFunc

	Recorder *progrock.Recorder

	httpClient *http.Client
	bkClient   *bkclient.Client
	bkSession  *bksession.Session

	hostname string
}

func Connect(ctx context.Context, params SessionParams) (_ *Session, rerr error) {
	s := &Session{SessionParams: params}

	if s.RouterID == "" {
		s.RouterID = identity.NewID()
	}

	// TODO:
	fmt.Fprintf(os.Stderr, "Connecting to router %s\n", params.RouterID)
	defer fmt.Fprintf(os.Stderr, "Connected to router %s\n", params.RouterID)

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

	internalCtx = engine.ContextWithClientMetadata(internalCtx, &engine.ClientMetadata{
		ClientID:       s.ID(),
		RouterID:       s.RouterID,
		ClientHostname: s.hostname,
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

	/* TODO: do we still need this? or covered serverside now?
	// oci stores
	ociStoreDir := filepath.Join(xdg.CacheHome, "dagger", "oci")
	ociStore, err := local.NewStore(ociStoreDir)
	if err != nil {
		return nil, fmt.Errorf("new local oci store: %w", err)
	}
	bkSession.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName: ociStore,
	}))
	*/

	// TODO: more export attachables

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
	/* TODO: fix by including engine name in the Info return, then we can get version easily too
	if s.EngineNameCallback != nil && bkClient.EngineName != "" {
		s.EngineNameCallback(bkClient.EngineName)
	}
	*/

	bkSession.Allow(progRockAttachable{progMultiW})
	recorder := progrock.NewRecorder(progMultiW)
	s.Recorder = recorder

	// start the router if it's not already running, client+router ID are
	// passed through grpc context
	s.eg.Go(func() error {
		_, err := s.bkClient.ControlClient().Solve(internalCtx, &controlapi.SolveRequest{})
		return err
	})

	// run the client session
	s.eg.Go(func() error {
		// client ID and router ID get passed via the session ID and session shared key respectively,
		// this is because in order to explicitly set those in our own code we'd need to copy
		// a lot of internal code; could be addressed with upstream tweaks to make some types
		// public
		return bkSession.Run(internalCtx, grpchijack.Dialer(s.bkClient.ControlClient()))
	})

	// Try connecting to the session server to make sure it's running
	s.httpClient = &http.Client{Transport: &http.Transport{DialContext: s.DialContext}}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	err = backoff.Retry(func() error {
		ctx, cancel := context.WithTimeout(ctx, bo.NextBackOff())
		defer cancel()
		err := s.Do(ctx, `{defaultPlatform}`, "", nil, nil)
		if err != nil {
			// TODO:
			fmt.Fprintf(os.Stderr, "connect err: %v\n", err)
		}
		return err
	}, backoff.WithContext(bo, ctx))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	return s, nil
}

func (s *Session) Close() error {
	// mark all groups completed
	// close the recorder so the UI exits
	s.Recorder.Complete()
	s.Recorder.Close()

	if s.internalCancel != nil {
		s.internalCancel()
		s.bkSession.Close()
		s.bkClient.Close()
		return s.eg.Wait()
	}
	return nil
}

func (s *Session) ID() string {
	return s.bkSession.ID()
}

func (s *Session) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return grpchijack.Dialer(s.bkClient.ControlClient())(ctx, "", engine.ClientMetadata{
		ClientID:       s.ID(),
		RouterID:       s.RouterID,
		ClientHostname: s.hostname,
	}.ToMD())
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

	if err := os.MkdirAll(dest, 0700); err != nil {
		return fmt.Errorf("failed to create synctarget dest dir %s: %w", dest, err)
	}
	return fsutil.Receive(stream.Context(), stream, dest, fsutil.ReceiveOpt{
		Merge: true,
		Filter: func() func(string, *fstypes.Stat) bool {
			uid := os.Getuid()
			gid := os.Getgid()
			return func(p string, st *fstypes.Stat) bool {
				st.Uid = uint32(uid)
				st.Gid = uint32(gid)
				return true
			}
		}(),
	})
}

type progRockAttachable struct {
	writer progrock.Writer
}

func (a progRockAttachable) Register(srv *grpc.Server) {
	progrock.RegisterProgressServiceServer(srv, progrock.NewRPCReceiver(a.writer))
}
