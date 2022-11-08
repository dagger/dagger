//go:generate cloak client-gen -o api.gen.go --package dagger
package dagger

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"dagger.io/dagger/internal/engineconn"
	_ "dagger.io/dagger/internal/engineconn/embedded" // embedded connection
	_ "dagger.io/dagger/internal/engineconn/http"     // http connection
	_ "dagger.io/dagger/internal/engineconn/unix"     // unix connection
	"dagger.io/dagger/internal/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"go.uber.org/multierr"
)

// Client is the Dagger Engine Client
type Client struct {
	Query

	conn    engineconn.EngineConn
	session *Session
	gql     graphql.Client
}

// ClientOpt holds a client option
type ClientOpt interface {
	setClientOpt(cfg *engineconn.Config)
}

type clientOptFunc func(cfg *engineconn.Config)

func (fn clientOptFunc) setClientOpt(cfg *engineconn.Config) {
	fn(cfg)
}

// WithWorkdir sets the engine workdir
func WithWorkdir(path string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.Workdir = path
	})
}

// WithConfigPath sets the engine config path
func WithConfigPath(path string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.ConfigPath = path
	})
}

// WithNoExtensions disables installing extensions
func WithNoExtensions() ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.NoExtensions = true
	})
}

// WithLogOutput sets the progress writer
func WithLogOutput(writer io.Writer) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.LogOutput = writer
	})
}

// Connect to a Dagger Engine
func Connect(ctx context.Context, opts ...ClientOpt) (_ *Client, rerr error) {
	defer func() {
		if rerr != nil {
			rerr = withErrorHelp(rerr)
		}
	}()

	cfg := &engineconn.Config{}

	for _, o := range opts {
		o.setClientOpt(cfg)
	}

	var err error
	cfg.Workdir, cfg.ConfigPath, err = NormalizePaths(cfg.Workdir, cfg.ConfigPath)
	if err != nil {
		return nil, err
	}

	// default host
	host := "embedded://"
	// if one is found in `DAGGER_HOST` -- use it instead
	if h := os.Getenv("DAGGER_HOST"); h != "" {
		host = h
	}
	conn, err := engineconn.Get(host)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn: conn,
	}
	dialer, err := c.conn.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	c.session, err = openSession(ctx, dialer, cfg.Workdir)
	if err != nil {
		return nil, fmt.Errorf("open sessionme: %w", err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _network, _addr string) (net.Conn, error) {
				return dialer(ctx)
			},
		},
	}
	c.gql = errorWrappedClient{graphql.NewClient(c.conn.Addr()+"/query?session="+c.session.ID, client)}
	c.Query = Query{
		q: querybuilder.Query(),
		c: c.gql,
	}
	if _, err := os.Stat(filepath.Join(cfg.Workdir, cfg.ConfigPath)); !cfg.NoExtensions && err == nil {
		err = c.installExtensions(ctx, cfg.ConfigPath)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (client *Client) installExtensions(ctx context.Context, configPath string) error {
	_, err := client.Host().Workdir().LoadProject(configPath).Install(ctx)
	return err
}

// Close the engine connection
func (client *Client) Close() error {
	return multierr.Append(
		// NB(vito): close session first so gateway can be closed and logs can drain
		client.session.Close(),
		client.conn.Close(),
	)
}

// Do sends a GraphQL request to the engine
func (client *Client) Do(ctx context.Context, req *Request, resp *Response) error {
	r := graphql.Response{}
	if resp != nil {
		r.Data = resp.Data
		r.Errors = resp.Errors
		r.Extensions = resp.Extensions
	}
	return client.gql.MakeRequest(ctx, &graphql.Request{
		Query:     req.Query,
		Variables: req.Variables,
		OpName:    req.OpName,
	}, &r)
}

// Request contains all the values required to build queries executed by
// the graphql.Client.
//
// Typically, GraphQL APIs will accept a JSON payload of the form
//
//	{"query": "query myQuery { ... }", "variables": {...}}`
//
// and Request marshals to this format.  However, MakeRequest may
// marshal the data in some other way desired by the backend.
type Request struct {
	// The literal string representing the GraphQL query, e.g.
	// `query myQuery { myField }`.
	Query string `json:"query"`
	// A JSON-marshalable value containing the variables to be sent
	// along with the query, or nil if there are none.
	Variables interface{} `json:"variables,omitempty"`
	// The GraphQL operation name. The server typically doesn't
	// require this unless there are multiple queries in the
	// document, but genqlient sets it unconditionally anyway.
	OpName string `json:"operationName"`
}

// Response that contains data returned by the GraphQL API.
//
// Typically, GraphQL APIs will return a JSON payload of the form
//
//	{"data": {...}, "errors": {...}}
//
// It may additionally contain a key named "extensions", that
// might hold GraphQL protocol extensions. Extensions and Errors
// are optional, depending on the values returned by the server.
type Response struct {
	Data       interface{}            `json:"data"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
	Errors     gqlerror.List          `json:"errors,omitempty"`
}

type errorWrappedClient struct {
	graphql.Client
}

func (c errorWrappedClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	err := c.Client.MakeRequest(ctx, req, resp)
	if err != nil {
		return withErrorHelp(err)
	}
	return nil
}

const (
	daggerJSONName = "dagger.json"
)

func NormalizePaths(workdir, configPath string) (string, string, error) {
	if workdir == "" {
		workdir = os.Getenv("DAGGER_WORKDIR")
	}
	if workdir == "" {
		var err error
		workdir, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", "", err
	}

	if configPath == "" {
		configPath = os.Getenv("DAGGER_CONFIG")
	}
	if configPath == "" {
		configPath = filepath.Join(workdir, daggerJSONName)
	}
	if !filepath.IsAbs(configPath) {
		var err error
		configPath, err = filepath.Abs(configPath)
		if err != nil {
			return "", "", err
		}
	}
	configPath, err = filepath.Rel(workdir, configPath)
	if err != nil {
		return "", "", err
	}
	return workdir, configPath, nil
}
