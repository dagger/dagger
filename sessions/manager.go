package sessions

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dagger.io/dagger/filesend"
	"github.com/dagger/dagger/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const (
	headerSessionID        = "X-Docker-Expose-Session-Uuid"
	headerSessionName      = "X-Docker-Expose-Session-Name"
	headerSessionSharedKey = "X-Docker-Expose-Session-Sharedkey"
	headerSessionMethod    = "X-Docker-Expose-Session-Grpc-Method"
)

type Manager struct {
	bkClient  *bkclient.Client
	solveCh   chan *bkclient.SolveStatus
	solveOpts bkclient.SolveOpt

	sessions        map[string]*clientSession
	mu              sync.Mutex
	updateCondition *sync.Cond

	gws  map[string]bkgw.Client
	gwsL sync.Mutex

	mirrors sync.WaitGroup
}

type clientSession struct {
	ctx       context.Context
	id        string
	name      string
	sharedKey string
	cc        *grpc.ClientConn
	methods   []string
	done      chan struct{}
}

func (s *clientSession) closed() bool {
	select {
	case <-s.ctx.Done():
		return true
	default:
		return false
	}
}

func NewManager(
	bkClient *bkclient.Client,
	solveCh chan *bkclient.SolveStatus,
	solveOpts bkclient.SolveOpt,
) *Manager {
	sm := &Manager{
		bkClient:  bkClient,
		solveCh:   solveCh,
		solveOpts: solveOpts,

		sessions: make(map[string]*clientSession),
		gws:      make(map[string]bkgw.Client),
	}
	sm.updateCondition = sync.NewCond(&sm.mu)
	return sm
}

func (manager *Manager) Wait() {
	manager.mirrors.Wait()
}

func (manager *Manager) HandleHTTPRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return errors.New("handler does not support hijack")
	}

	id := r.Header.Get(headerSessionID)

	proto := r.Header.Get("Upgrade")

	manager.mu.Lock()
	if _, ok := manager.sessions[id]; ok {
		manager.mu.Unlock()
		return errors.Errorf("session %s already exists", id)
	}

	if proto == "" {
		manager.mu.Unlock()
		return errors.New("no upgrade proto in request")
	}

	if proto != "h2c" {
		manager.mu.Unlock()
		return errors.Errorf("protocol %s not supported", proto)
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		manager.mu.Unlock()
		return errors.Wrap(err, "failed to hijack connection")
	}

	resp := &http.Response{
		StatusCode: http.StatusSwitchingProtocols,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
	}
	resp.Header.Set("Connection", "Upgrade")
	resp.Header.Set("Upgrade", proto)

	// set raw mode
	conn.Write([]byte{})
	resp.Write(conn)

	return manager.handleConn(ctx, conn, r.Header)
}

func (manager *Manager) client(ctx context.Context, id string, noWait bool) (*clientSession, error) {
	if id == "" {
		debug.PrintStack()
		return nil, fmt.Errorf("no session id provided")
	}
	// session prefix is used to identify vertexes with different contexts so
	// they would not collide, but for lookup we don't need the prefix
	if p := strings.SplitN(id, ":", 2); len(p) == 2 && len(p[1]) > 0 {
		id = p[1]
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		manager.mu.Lock()
		manager.updateCondition.Broadcast()
		manager.mu.Unlock()
	}()

	var c *clientSession

	manager.mu.Lock()
	for {
		select {
		case <-ctx.Done():
			manager.mu.Unlock()
			return nil, errors.Wrapf(ctx.Err(), "no active session for %s", id)
		default:
		}
		var ok bool
		c, ok = manager.sessions[id]
		if (!ok || c.closed()) && !noWait {
			manager.updateCondition.Wait()
			continue
		}
		manager.mu.Unlock()
		break
	}

	if c == nil {
		return nil, nil
	}

	return c, nil
}

// caller needs to take lock, this function will release it
func (manager *Manager) handleConn(ctx context.Context, conn net.Conn, opts map[string][]string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	opts = canonicalHeaders(opts)

	h := http.Header(opts)
	id := h.Get(headerSessionID)
	name := h.Get(headerSessionName)
	sharedKey := h.Get(headerSessionSharedKey)

	ctx, cc, err := grpcClientConn(ctx, conn)
	if err != nil {
		manager.mu.Unlock()
		return err
	}

	c := &clientSession{
		ctx:       ctx,
		id:        id,
		name:      name,
		sharedKey: sharedKey,
		cc:        cc,
		methods:   opts[headerSessionMethod],
		done:      make(chan struct{}),
	}

	manager.sessions[id] = c
	manager.updateCondition.Broadcast()
	manager.mu.Unlock()

	defer func() {
		manager.mu.Lock()
		delete(manager.sessions, id)
		manager.mu.Unlock()
	}()

	<-c.ctx.Done()
	conn.Close()
	close(c.done)

	return nil
}

func (manager *Manager) TarSend(ctx context.Context, id string, path string, unpack bool) (io.WriteCloser, error) {
	caller, err := manager.client(ctx, id, false)
	if err != nil {
		return nil, err
	}

	fs := filesend.NewFileSendClient(caller.cc)

	tarClient, err := fs.TarStream(ctx)
	if err != nil {
		return nil, err
	}

	err = tarClient.Send(&filesend.StreamMessage{
		Message: &filesend.StreamMessage_Init{
			Init: &filesend.InitMessage{
				Dest:   path,
				Unpack: unpack,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return &fileSendWriter{client: tarClient}, nil
}

type fileSendWriter struct {
	client   filesend.FileSend_TarStreamClient
	closed   bool
	closeErr error
}

func (w *fileSendWriter) Write(b []byte) (int, error) {
	err := w.client.Send(&filesend.StreamMessage{
		Message: &filesend.StreamMessage_Bytes{
			Bytes: &filesend.BytesMessage{
				Data: b,
			},
		},
	})
	if err != nil {
		return 0, err
	}

	return len(b), nil
}

func (w *fileSendWriter) Close() error {
	if w.closed {
		return w.closeErr
	}

	w.closed = true
	_, w.closeErr = w.client.CloseAndRecv()
	return w.closeErr
}

func (manager *Manager) solveOpt(ctx context.Context, id string, secretStore *secret.Store, caller *clientSession) bkclient.SolveOpt {
	solveOpts := manager.solveOpts

	solveOpts.Session = append(solveOpts.Session,
		secretsprovider.NewSecretProvider(secretStore),
		clientProxy{caller},
	)

	return solveOpts
}

func (manager *Manager) Export(ctx context.Context, id string, ex bkclient.ExportEntry, fn bkgw.BuildFunc) error {
	caller, err := manager.client(ctx, id, false)
	if err != nil {
		return err
	}

	ch := manager.mirrorCh("export:" + id)

	secretStore := secret.NewStore()
	solveOpt := manager.solveOpt(ctx, id, secretStore, caller)

	solveOpt.Exports = []bkclient.ExportEntry{ex}

	_, err = manager.bkClient.Build(context.Background(), solveOpt, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		// Secret store is a circular dependency, since it needs to resolve
		// SecretIDs using the gateway, we don't have a gateway until we call
		// Build, which needs SolveOpts, which needs to contain the secret store.
		//
		// Thankfully we can just yeet the gateway into the store.
		secretStore.SetGateway(gw)

		return fn(ctx, gw)
	}, ch)

	log.Println("BUILD RESULT", err)
	return err
}

func (manager *Manager) Gateway(ctx context.Context, id string) (bkgw.Client, error) {
	caller, err := manager.client(ctx, id, false)
	if err != nil {
		return nil, err
	}

	// FIXME(vito): per-ID lock; this is a bit of a bottleneck
	log.Println("LOCKING")
	manager.gwsL.Lock()
	defer manager.gwsL.Unlock()
	defer log.Println("UNLOCKING")

	gw, ok := manager.gws[id]
	if ok {
		return gw, nil
	}

	ch := manager.mirrorCh("gw:" + id)

	secretStore := secret.NewStore()
	solveOpt := manager.solveOpt(ctx, id, secretStore, caller)

	gwCh := make(chan bkgw.Client, 1)
	gwErrCh := make(chan error, 1)
	go func() {
		_, gwErr := manager.bkClient.Build(context.Background(), solveOpt, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			// Secret store is a circular dependency, since it needs to resolve
			// SecretIDs using the gateway, we don't have a gateway until we call
			// Build, which needs SolveOpts, which needs to contain the secret store.
			//
			// Thankfully we can just yeet the gateway into the store.
			secretStore.SetGateway(gw)

			gwCh <- gw

			// wait for client to go away
			<-caller.ctx.Done()

			return nil, nil
		}, ch)

		gwErrCh <- gwErr

		// clean up completed gateway
		manager.gwsL.Lock()
		delete(manager.gws, id)
		manager.gwsL.Unlock()
	}()

	select {
	case gw := <-gwCh:
		manager.gws[id] = gw
		return gw, nil
	case err := <-gwErrCh:
		return nil, err
	}
}

// mirrorCh mirrors messages from one channel to another, protecting the
// destination channel from being closed.
//
// this is used to reflect Build/Solve progress in a longer-lived progress UI,
// since they close the channel when they're done.
func (manager *Manager) mirrorCh(tag string) chan *bkclient.SolveStatus {
	if manager.solveCh == nil {
		return nil
	}

	mirrorCh := make(chan *bkclient.SolveStatus)

	log.Println("!!!!! MIRRORCH ADD", tag)
	manager.mirrors.Add(1)
	go func() {
		defer manager.mirrors.Done()
		defer log.Println("!!!!! MIRRORCH DONE", tag)
		for event := range mirrorCh {
			manager.solveCh <- event
		}
	}()

	return mirrorCh
}

type clientProxy struct {
	client *clientSession
}

func (p clientProxy) Register(srv *grpc.Server) {
	director := proxy.DefaultDirector(p.client.cc)

	svcMethods := map[string][]string{}
	for _, method := range p.client.methods {
		segments := strings.Split(method, "/")
		svc := segments[1]
		method := segments[2]
		svcMethods[svc] = append(svcMethods[svc], method)
	}

	svcs := srv.GetServiceInfo()
	for svc, methods := range svcMethods {
		_, exists := svcs[svc]
		if exists {
			// avoid duplicate registration for e.g. grpc.health.v1.Health
			if svc != "grpc.health.v1.Health" {
				log.Println("WARNING: skipping unknown duplicate service:", svc)
			}
		} else {
			proxy.RegisterService(srv, director, svc, methods...)
		}
	}
}

func canonicalHeaders(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k := range in {
		out[http.CanonicalHeaderKey(k)] = in[k]
	}
	return out
}

func grpcClientConn(ctx context.Context, conn net.Conn) (context.Context, *grpc.ClientConn, error) {
	var dialCount int64
	dialer := grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		if c := atomic.AddInt64(&dialCount, 1); c > 1 {
			return nil, errors.Errorf("only one connection allowed")
		}
		return conn, nil
	})

	dialOpts := []grpc.DialOption{
		dialer,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	cc, err := grpc.DialContext(ctx, "localhost", dialOpts...)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create grpc client")
	}

	ctx, cancel := context.WithCancel(ctx)
	go monitorHealth(ctx, cc, cancel)

	return ctx, cc, nil
}

func monitorHealth(ctx context.Context, cc *grpc.ClientConn, cancelConn func()) {
	defer cancelConn()
	defer cc.Close()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	healthClient := grpc_health_v1.NewHealthClient(cc)

	failedBefore := false
	consecutiveSuccessful := 0
	defaultHealthcheckDuration := 30 * time.Second
	lastHealthcheckDuration := time.Duration(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// This healthcheck can erroneously fail in some instances, such as receiving lots of data in a low-bandwidth scenario or too many concurrent builds.
			// So, this healthcheck is purposely long, and can tolerate some failures on purpose.

			healthcheckStart := time.Now()

			timeout := time.Duration(math.Max(float64(defaultHealthcheckDuration), float64(lastHealthcheckDuration)*1.5))
			ctx, cancel := context.WithTimeout(ctx, timeout)
			_, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
			cancel()

			lastHealthcheckDuration = time.Since(healthcheckStart)

			if err != nil {
				if failedBefore {
					log.Println("healthcheck failed fatally:", err)
					return
				}

				failedBefore = true
				consecutiveSuccessful = 0
				log.Println("healthcheck failed:", err)
			} else {
				consecutiveSuccessful++

				if consecutiveSuccessful >= 5 && failedBefore {
					failedBefore = false
					log.Println("reset healthcheck failure")
				}

				log.Println("healthcheck ok")
			}
		}
	}
}
