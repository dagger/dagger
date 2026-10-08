package dagger

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"dagger.io/dagger/engineconn"
)

// Client is a connection to a Dagger Engine.
//
// This type is transport-only: it holds the engine connection and the raw
// GraphQL client. The generated API bindings (Container, Directory, and so
// on) live in dagger.io/dagger/core, which builds its own query root on top
// of a *Client via core.NewQuery. That split avoids an import cycle: core
// needs to import dagger.io/dagger, so dagger.io/dagger cannot import core
// back.
type Client struct {
	conn   engineconn.EngineConn
	client graphql.Client
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

// WithWorkspace sets the workspace binding for the engine session.
//
// The ref may be either a local path or a remote git ref.
//
// This only has effect when connecting via the CLI.
func WithWorkspace(ref string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.Workspace = ref
	})
}

// WithLogOutput sets the progress writer
func WithLogOutput(writer io.Writer) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.LogOutput = writer
	})
}

// WithLoadWorkspaceModules opts this client into loading workspace modules
// based on the working directory when the session is created via the CLI.
func WithLoadWorkspaceModules() ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.LoadWorkspaceModules = true
	})
}

// WithConn sets the engine connection explicitly
func WithConn(conn engineconn.EngineConn) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.Conn = conn
	})
}

// WithVersionOverride requests a specific schema version from the engine.
// Calling this may cause the schema to be out-of-sync from the codegen - this
// option is likely *not* desirable for most use cases.
//
// This only has effect when connecting via the CLI, and is only exposed for
// testing purposes.
func WithVersionOverride(version string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.VersionOverride = version
	})
}

// WithVerbosity sets the verbosity level for the progress output
func WithVerbosity(level int) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.Verbosity = level
	})
}

// WithRunnerHost sets the runner host URL for provisioning and connecting to
// an engine.
//
// This only has effect when connecting via the CLI, and is only exposed for
// testing purposes.
func WithRunnerHost(runnerHost string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.RunnerHost = runnerHost
	})
}

// Set this additional environment variable in the CLI subprocess for the session
func WithEnvironmentVariable(key, value string) ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.ExtraEnv = append(cfg.ExtraEnv, key+"="+value)
	})
}

// Deprecated: workspace modules are core-only by default. Use
// WithLoadWorkspaceModules to opt into loading them when needed.
func WithSkipWorkspaceModules() ClientOpt {
	return clientOptFunc(func(cfg *engineconn.Config) {
		cfg.SkipWorkspaceModules = true
	})
}

// Connect to a Dagger Engine
func Connect(ctx context.Context, opts ...ClientOpt) (*Client, error) {
	cfg := &engineconn.Config{}

	for _, o := range opts {
		o.setClientOpt(cfg)
	}

	conn, err := engineconn.Get(ctx, cfg)
	if err != nil {
		return nil, err
	}
	gql := errorWrappedClient{graphql.NewClient("http://"+conn.Host()+"/query", conn)}

	c := &Client{
		client: gql,
		conn:   conn,
	}
	return c, nil
}

// GraphQLClient returns the underlying graphql.Client
func (c *Client) GraphQLClient() graphql.Client {
	return c.client
}

// Close the engine connection
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

var (
	defaultClient   *Client
	defaultClientMu sync.Mutex
)

// Default returns a process-wide shared engine connection, connecting lazily
// on first use. Every caller that doesn't need a distinct, explicitly
// configured connection should go through Default, so that (outside of an
// inherited session) they all end up sharing the same engine session rather
// than each provisioning their own.
func Default(ctx context.Context) (*Client, error) {
	defaultClientMu.Lock()
	defer defaultClientMu.Unlock()

	if defaultClient == nil {
		c, err := Connect(ctx, WithLogOutput(os.Stdout))
		if err != nil {
			return nil, err
		}
		defaultClient = c
	}
	return defaultClient, nil
}

// Close closes the default engine connection returned by Default, if one has
// been established.
func Close() error {
	defaultClientMu.Lock()
	defer defaultClientMu.Unlock()

	var err error
	if defaultClient != nil {
		err = defaultClient.Close()
		defaultClient = nil
	}
	return err
}

// Do sends a GraphQL request to the engine
func (c *Client) Do(ctx context.Context, req *Request, resp *Response) error {
	r := graphql.Response{}
	if resp != nil {
		r.Data = resp.Data
		r.Errors = resp.Errors
		r.Extensions = resp.Extensions
	}
	return c.client.MakeRequest(ctx, &graphql.Request{
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

// errorWrappedClient classifies GraphQL errors into more specific error
// types (see getCustomError) as they come back from the engine. It lives
// here, rather than in dagger.io/dagger/core alongside the generated types,
// because dagger.io/dagger/core needs to import dagger.io/dagger, and
// classifying a transport-level error doesn't need any generated type
// anyway.
type errorWrappedClient struct {
	graphql.Client
}

func (c errorWrappedClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	err := c.Client.MakeRequest(ctx, req, resp)
	if err != nil {
		if e := getCustomError(err); e != nil {
			return e
		}
		return err
	}
	return nil
}

type gqlExtendedError struct {
	inner *gqlerror.Error
}

// Same as telemetry.ExtendedError, but without the dependency, to simplify
// client generation.
type extendedError interface {
	error
	Extensions() map[string]any
}

func (e gqlExtendedError) Unwrap() error {
	return e.inner
}

var _ extendedError = gqlExtendedError{}

func (e gqlExtendedError) Error() string {
	return e.inner.Message
}

func (e gqlExtendedError) Extensions() map[string]any {
	return e.inner.Extensions
}

// getCustomError parses a GraphQL error into a more specific error type.
func getCustomError(err error) error {
	var gqlErr *gqlerror.Error
	if !errors.As(err, &gqlErr) {
		return nil
	}

	ext := gqlErr.Extensions

	lessNoisyErr := gqlExtendedError{gqlErr}

	typ, ok := ext["_type"].(string)
	if !ok {
		return lessNoisyErr
	}

	if typ == "EXEC_ERROR" {
		e := &ExecError{
			original: lessNoisyErr,
		}
		if code, ok := ext["exitCode"].(float64); ok {
			e.ExitCode = int(code)
		}
		if args, ok := ext["cmd"].([]interface{}); ok {
			cmd := make([]string, len(args))
			for i, v := range args {
				cmd[i] = v.(string)
			}
			e.Cmd = cmd
		}
		if stdout, ok := ext["stdout"].(string); ok {
			e.Stdout = stdout
		}
		if stderr, ok := ext["stderr"].(string); ok {
			e.Stderr = stderr
		}
		return e
	}

	return lessNoisyErr
}

// ExecError is an API error from an exec operation.
type ExecError struct {
	original extendedError
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

var _ extendedError = (*ExecError)(nil)

func (e *ExecError) Error() string {
	return e.Message()
}

func (e *ExecError) Extensions() map[string]any {
	return e.original.Extensions()
}

func (e *ExecError) Message() string {
	return e.original.Error()
}

func (e *ExecError) Unwrap() error {
	return e.original
}
