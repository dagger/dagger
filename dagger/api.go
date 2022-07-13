package dagger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"

	"github.com/graphql-go/graphql"
	"github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newAPIServer(control *client.Client, gw bkgw.Client, sp *secretProvider) *apiServer {
	s := &apiServer{
		control:        control,
		gw:             gw,
		refs:           make(map[string]bkgw.Reference),
		secretProvider: sp,
	}
	return s
}

type apiServer struct {
	control        *client.Client
	gw             bkgw.Client
	refs           map[string]bkgw.Reference
	secretProvider *secretProvider
}

func (s *apiServer) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	go (&http.Server{Handler: s}).Serve(&singleConnListener{Conn: conn})
	<-ctx.Done()
}

func (s *apiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/graphql" {
		http.NotFound(w, r)
		return
	}
	payload := r.URL.Query().Get("payload")
	result := graphql.Do(graphql.Params{
		Schema:        schema,
		RequestString: payload,
		Context:       withPayload(withGatewayClient(r.Context(), s.gw), payload),
	})
	if result.HasErrors() {
		http.Error(w, fmt.Sprintf("unexpected errors: %v", result.Errors), http.StatusInternalServerError)
		return
	}
	if err := json.NewEncoder(w).Encode(result.Data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

type gatewayClientKey struct{}

func withGatewayClient(ctx context.Context, gw bkgw.Client) context.Context {
	return context.WithValue(ctx, gatewayClientKey{}, gw)
}

func getGatewayClient(ctx context.Context) (bkgw.Client, error) {
	v := ctx.Value(gatewayClientKey{})
	if v == nil {
		return nil, fmt.Errorf("no gateway client")
	}
	return v.(bkgw.Client), nil
}

// TODO: feel like there's probably a better of getting this in the resolver funcs, but couldn't find it
type payloadKey struct{}

func withPayload(ctx context.Context, payload string) context.Context {
	return context.WithValue(ctx, payloadKey{}, payload)
}

func getPayload(ctx context.Context) string {
	v := ctx.Value(payloadKey{})
	if v == nil {
		return ""
	}
	return v.(string)
}

type APIClient interface {
	Do(ctx context.Context, payload string) (string, error)
}

func doRequest(ctx context.Context, payload string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://fake.invalid/graphql", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("payload", payload)
	req.URL.RawQuery = q.Encode()
	return req, nil
}

type httpClient struct {
	*http.Client
}

var _ APIClient = httpClient{}

func (c httpClient) Do(ctx context.Context, payload string) (string, error) {
	req, err := doRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type clientAdapter struct {
	*apiServer
}

var _ APIClient = clientAdapter{}

func (c clientAdapter) Do(ctx context.Context, payload string) (string, error) {
	req, err := doRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func newAPISocketProvider() *apiSocketProvider {
	return &apiSocketProvider{}
}

type apiSocketProvider struct {
	api *apiServer
}

func (p *apiSocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p *apiSocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *apiSocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no metadata in context")
	}
	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.Internal, "no sshid in metadata")
	}
	id := v[0]
	if id != daggerSockName {
		return status.Errorf(codes.Internal, "no api connection for id %s", id)
	}

	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.api.serve(ctx, serverConn) // TODO: better synchronization
	return sshforward.Copy(context.TODO(), clientConn, stream, nil)
}

type singleConnListener struct {
	net.Conn
}

// Accept implements net.Listener
func (l *singleConnListener) Accept() (net.Conn, error) {
	return l.Conn, nil
}

// Addr implements net.Listener
func (l *singleConnListener) Addr() net.Addr {
	return l.LocalAddr()
}

// Close implements net.Listener
func (l *singleConnListener) Close() error {
	return l.Conn.Close()
}
