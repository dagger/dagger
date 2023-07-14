package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/internal/handler"
	"github.com/dagger/dagger/engine/session"
	"github.com/dagger/graphql"
	"github.com/dagger/graphql/gqlerrors"
	"github.com/moby/buildkit/frontend"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/worker"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

const SessionIDHeader = "X-Dagger-Session-ID"

type Server struct {
	*FrontendOpts
	llbBridge      frontend.FrontendLLBBridge
	worker         worker.Worker
	sessionManager *session.Manager
	bkClient       *buildkit.Client

	startOnce sync.Once
	eg        errgroup.Group

	// client session id -> client session
	connectedClients map[string]bksession.Caller
	clientConnMu     sync.Mutex

	schema   *schema.MergedSchemas
	recorder *progrock.Recorder
}

func (s *Server) Run(ctx context.Context) (*frontend.Result, error) {
	if err := s.addClient(ctx, s.ClientSessionID); err != nil {
		return nil, err
	}
	defer s.removeClient(s.ClientSessionID)

	s.startOnce.Do(func() {
		s.eg.Go(func() error {
			clientCaller, ok := s.connectedClients[s.ClientSessionID]
			if !ok {
				return fmt.Errorf("no client with id %s", s.ClientSessionID)
			}
			clientConn := clientCaller.Conn()
			progClient := progrock.NewProgressServiceClient(clientConn)
			progUpdates, err := progClient.WriteUpdates(ctx)
			if err != nil {
				return err
			}

			progSockPath := fmt.Sprintf("/run/dagger/server-progrock-%s.sock", s.ServerID)
			progWriter, progCleanup, err := progrockForwarder(progSockPath, progrock.MultiWriter{
				&progrock.RPCWriter{Conn: clientConn, Updates: progUpdates},
				progrockLogrusWriter{},
			})
			if err != nil {
				return err
			}
			defer progCleanup()

			// TODO: correct progrock labels
			go pipeline.LoadRootLabels("/", "da-engine")
			// s.recorder = progrock.NewRecorder(progrockLogrusWriter{}, progrock.WithLabels(labels...))
			s.recorder = progrock.NewRecorder(progWriter)
			ctx = progrock.RecorderToContext(ctx, s.recorder)
			defer func() {
				// mark all groups completed
				s.recorder.Complete()
				// close the recorder so the UI exits
				s.recorder.Close()
			}()

			s.bkClient = buildkit.NewClient(
				s.llbBridge,
				s.worker,
				s.sessionManager,
				// TODO: cache config
				"", nil,
			)

			apiSchema, err := schema.New(schema.InitializeArgs{
				BuildkitClient: s.bkClient,
				Platform:       s.worker.Platforms(true)[0],
				ProgSockPath:   progSockPath,
				OCIStore:       s.worker.ContentStore(),
				LeaseManager:   s.worker.LeaseManager(),
				/* TODO:
				Auth     *auth.RegistryAuthProvider
				Secrets  *session.SecretStore
				*/
			})
			if err != nil {
				return err
			}
			s.schema = apiSchema

			serverSockPath := s.ServerSockPath()
			if err := os.MkdirAll(filepath.Dir(serverSockPath), 0700); err != nil {
				return err
			}

			// TODO:
			bklog.G(ctx).Debugf("listening on %s", serverSockPath)

			l, err := net.Listen("unix", serverSockPath)
			if err != nil {
				return err
			}
			defer l.Close()
			srv := http.Server{
				Handler:           s,
				ReadHeaderTimeout: 30 * time.Second,
			}
			go func() {
				<-ctx.Done()
				l.Close()
			}()
			err = srv.Serve(l)
			// if error is "use of closed network connection", it's from the context being canceled
			if err != nil && !errors.Is(err, net.ErrClosed) {
				return err
			}
			return nil
		})
	})

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- s.eg.Wait()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-waitCh:
		// TODO: re-add support for the combined result needed when upstream caching enabled
		return nil, err
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Add("x-dagger-engine", engine.Version)

	/* TODO: re-add session token
	if s.sessionToken != "" {
		username, _, ok := req.BasicAuth()
		if !ok || username != s.sessionToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="Access to the Dagger engine session"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	*/

	defer func() {
		if v := recover(); v != nil {
			msg := "Internal Server Error"
			code := http.StatusInternalServerError
			switch v := v.(type) {
			case error:
				msg = v.Error()
				if errors.As(v, &schema.InvalidInputError{}) {
					// panics can happen on invalid input in scalar serde
					code = http.StatusBadRequest
				}
			case string:
				msg = v
			}
			res := graphql.Result{
				Errors: []gqlerrors.FormattedError{
					gqlerrors.NewFormattedError(msg),
				},
			}
			bytes, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			http.Error(w, string(bytes), code)
		}
	}()

	req = req.WithContext(progrock.RecorderToContext(req.Context(), s.recorder))

	// TODO: should think through case where evil client lies about its session ID
	// maybe maintain a map of secret session token -> session ID and verify against that
	requesterSessionID := req.Header.Get(SessionIDHeader)
	req = req.WithContext(session.ContextWithSessionMetadata(req.Context(), s.ServerID, requesterSessionID))

	mux := http.NewServeMux()
	mux.Handle("/query", handler.New(&handler.Config{
		Schema: s.schema.Schema(),
	}))
	mux.ServeHTTP(w, req)
}

func (s *Server) addClient(ctx context.Context, clientSessionID string) error {
	caller, err := s.sessionManager.GetCaller(ctx, clientSessionID)
	if err != nil {
		return err
	}
	s.clientConnMu.Lock()
	defer s.clientConnMu.Unlock()
	s.connectedClients[clientSessionID] = caller
	return nil
}

func (s *Server) removeClient(clientSessionID string) error {
	s.clientConnMu.Lock()
	defer s.clientConnMu.Unlock()
	delete(s.connectedClients, clientSessionID)
	// TODO: need to close the caller conn here? or is that already done elsewhere?
	return nil
}
