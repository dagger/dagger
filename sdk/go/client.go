//go:generate dagger client-gen -o api.gen.go
package dagger

import (
	"context"
	"io"
	"os"

	"dagger.io/dagger/internal/engineconn"
	_ "dagger.io/dagger/internal/engineconn/embedded" // embedded connection
	_ "dagger.io/dagger/internal/engineconn/unix"     // unix connection
	"dagger.io/dagger/internal/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Client is the Dagger Engine Client
type Client struct {
	Query

	conn engineconn.EngineConn
	gql  graphql.Client
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

// WithLocalDir maps a local directory to the engine
func WithLocalDir(id, path string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		if cfg.LocalDirs == nil {
			cfg.LocalDirs = make(map[string]string)
		}
		cfg.LocalDirs[id] = path
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
func Connect(ctx context.Context, opts ...ClientOpt) (*Client, error) {
	cfg := &engineconn.Config{}

	for _, o := range opts {
		o.setClientOpt(cfg)
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
	client, err := c.conn.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	c.gql = graphql.NewClient("http://dagger/query", client)
	c.Query = Query{
		q: querybuilder.Query(),
		c: c.gql,
	}
	return c, nil
}

// Close the engine connection
func (c *Client) Close() error {
	return c.conn.Close()
}

// Do sends a GraphQL request to the engine
func (c *Client) Do(ctx context.Context, req *Request, resp *Response) error {
	r := graphql.Response{}
	if resp != nil {
		r.Data = resp.Data
		r.Errors = resp.Errors
		r.Extensions = resp.Extensions
	}
	return c.gql.MakeRequest(ctx, &graphql.Request{
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
