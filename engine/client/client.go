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
	"path/filepath"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/adrg/xdg"
	"github.com/cenkalti/backoff/v4"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/session"
	"github.com/dagger/dagger/telemetry"
	"github.com/docker/cli/cli/config"
	controlapi "github.com/moby/buildkit/api/services/control"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/connhelper"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/filesync"
	"github.com/moby/buildkit/session/grpchijack"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const OCIStoreName = "dagger-oci"

type SessionParams struct {
	// The id of the frontend server to connect to, or if blank a new one
	// will be started.
	ServerID string

	// TODO: re-add support
	SecretToken string

	// TODO: the difference between these two makes sense if you really think about it, but you shouldn't need to think about it
	RunnerHost string // host of dagger engine runner serving buildkit apis
	DaggerHost string // host of existing dagger graphql server to connect to (optional)
	UserAgent  string

	DisableHostRW bool

	JournalFile        string
	ProgrockWriter     progrock.Writer
	EngineNameCallback func(string)
	CloudURLCallback   func(string)
}

type Session struct {
	SessionParams
	eg             *errgroup.Group
	internalCancel context.CancelFunc

	Recorder *progrock.Recorder

	httpClient *DoerWithHeaders
	bkClient   *bkClient
	bkSession  *bksession.Session
}

func Connect(ctx context.Context, params SessionParams) (_ *Session, rerr error) {
	s := &Session{SessionParams: params}

	if s.ServerID == "" {
		s.ServerID = identity.NewID()
	}

	// TODO: this is only needed temporarily to work around issue w/
	// `dagger do` and `dagger project` not picking up env set by nesting
	// (which impacts project tests). Remove ASAP
	if v, ok := os.LookupEnv("_DAGGER_SERVER_ID"); ok {
		s.ServerID = v
	}
	if v, ok := os.LookupEnv("_EXPERIMENTAL_DAGGER_HOST"); ok {
		s.DaggerHost = v
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

	bkSessionName := identity.NewID() // TODO: does this affect anything?
	sharedKey := ""

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

	// filesync
	if !s.DisableHostRW {
		bkSession.Allow(filesync.NewFSSyncProvider(AnyDirSource{}))
		bkSession.Allow(AnyDirTarget{})
	}

	// secrets
	secretStore := core.NewSecretStore()
	bkSession.Allow(secretsprovider.NewSecretProvider(secretStore))
	// TODO: secretStore.SetGateway(...)

	// sockets
	bkSession.Allow(session.MergedSocketProvider{
		// TODO: enforce this in the session stream proxy
		// EnableHostNetworkAccess: !s.DisableHostRW,
	})

	// registry auth
	bkSession.Allow(auth.NewRegistryAuthProvider(config.LoadDefaultConfigFile(os.Stderr)))

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
	if s.EngineNameCallback != nil && bkClient.EngineName != "" {
		s.EngineNameCallback(bkClient.EngineName)
	}

	bkSession.Allow(progRockAttachable{progMultiW})
	recorder := progrock.NewRecorder(progMultiW)
	s.Recorder = recorder

	solveCh := make(chan *bkclient.SolveStatus)
	s.eg.Go(func() error {
		for ev := range solveCh {
			if err := recorder.Record(bk2progrock(ev)); err != nil {
				return fmt.Errorf("record: %w", err)
			}
		}
		return nil
	})

	// run the client s
	s.eg.Go(func() error {
		return bkSession.Run(internalCtx, grpchijack.Dialer(s.bkClient.ControlClient()))
	})

	var allowedEntitlements []entitlements.Entitlement
	if bkClient.PrivilegedExecEnabled {
		// NOTE: this just allows clients to set this if they want. It also needs
		// to be set in the ExecOp LLB and enabled server-side in order for privileged
		// execs to actually run.
		allowedEntitlements = append(allowedEntitlements, entitlements.EntitlementSecurityInsecure)
	}

	frontendOptMap, err := s.FrontendOpts().ToSolveOpts()
	if err != nil {
		return nil, fmt.Errorf("frontend opts: %w", err)
	}

	// start the session server frontend if it's not already running
	solveRef := identity.NewID()
	s.eg.Go(func() error {
		_, err := s.bkClient.ControlClient().Solve(internalCtx, &controlapi.SolveRequest{
			Ref:           solveRef,
			Session:       s.bkClient.DaggerFrontendSessionID,
			Frontend:      server.DaggerFrontendName,
			FrontendAttrs: frontendOptMap,
			Entitlements:  allowedEntitlements,
			Internal:      true, // disables history recording, which we don't need
			// TODO:
			// Cache: (for upstream remotecache)
		})
		return err
	})

	// connect to the progress stream from buildkit
	// TODO: upstream has a hardcoded 3 second sleep before cancelling this one's context, needed here?
	s.eg.Go(func() error {
		defer close(solveCh)
		stream, err := s.bkClient.ControlClient().Status(internalCtx, &controlapi.StatusRequest{
			Ref: solveRef,
		})
		if err != nil {
			return fmt.Errorf("failed to get status: %w", err)
		}
		for {
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("failed to receive status: %w", err)
			}
			solveCh <- bkclient.NewSolveStatus(resp)
		}
	})

	// Try connecting to the session server to make sure it's running
	s.httpClient = &DoerWithHeaders{
		inner: &http.Client{
			Transport: &http.Transport{
				DialContext: s.DialContext,
			},
		},
		headers: http.Header{
			server.SessionIDHeader: []string{bkSession.ID()},
		},
	}

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	err = backoff.Retry(func() error {
		return s.Do(ctx, `{defaultPlatform}`, "", nil, nil)
	}, backoff.WithContext(bo, ctx))
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	return s, nil
}

func (s *Session) FrontendOpts() server.FrontendOpts {
	return server.FrontendOpts{
		ServerID:        s.ServerID,
		ClientSessionID: s.bkSession.ID(),
		// TODO: cache configs
	}
}

func (s *Session) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	host := s.RunnerHost
	if s.DaggerHost != "" {
		host = s.DaggerHost
	}

	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parse runner host: %w", err)
	}
	switch u.Scheme {
	case "tcp":
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(10 * time.Second)
		}
		return net.DialTimeout("tcp", u.Host, time.Until(deadline))
	case "unix":
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(10 * time.Second)
		}
		return net.DialTimeout("unix", u.Path, time.Until(deadline))
	default:
	}

	queryStrs := u.Query()
	queryStrs.Add("addr", s.FrontendOpts().ServerAddr())
	u.RawQuery = queryStrs.Encode()
	connHelper, err := connhelper.GetConnectionHelper(u.String())
	if err != nil {
		return nil, fmt.Errorf("get connection helper: %w", err)
	}
	if connHelper == nil {
		return nil, fmt.Errorf("unsupported scheme in %s", u.String())
	}
	return connHelper.ContextDialer(ctx, "")
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
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("io copy: " + err.Error()))
		return
	}
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
	}
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

type DoerWithHeaders struct {
	inner   *http.Client
	headers http.Header
}

func (c DoerWithHeaders) Do(req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		req.Header[k] = v
	}
	return c.inner.Do(req)
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

	destVal, ok := opts[session.LocalDirExportDestPathMetaKey]
	if !ok {
		return fmt.Errorf("missing " + session.LocalDirExportDestPathMetaKey)
	}
	if len(destVal) != 1 {
		return fmt.Errorf("expected exactly one "+session.LocalDirExportDestPathMetaKey+" value, got %d", len(destVal))
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
