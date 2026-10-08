package dagger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ErrClientClosed is returned when a query uses an explicitly closed connection.
var ErrClientClosed = errors.New("dagger client is closed")

type moduleSpec struct{ address, pin string }
type moduleLoad struct {
	done chan struct{}
	err  error
}

// DefaultGraphQLClient returns a transport backed by the shared default session.
// Constructing it does not connect; the first request uses its context to connect.
func DefaultGraphQLClient() graphql.Client { return defaultGraphQLClient{} }

type defaultGraphQLClient struct{}

func (defaultGraphQLClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	client, err := Default(ctx)
	if err != nil {
		return err
	}
	return client.GraphQLClient().MakeRequest(ctx, req, resp)
}

type connectionGraphQLClient struct{ client *Client }

func (g connectionGraphQLClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	g.client.modulesMu.Lock()
	closed := g.client.closed
	g.client.modulesMu.Unlock()
	if closed {
		return ErrClientClosed
	}
	return g.client.client.MakeRequest(ctx, req, resp)
}

// ModuleGraphQLClient builds a transport for generated module bindings. It
// borrows client, or the default session when client is nil. The module is
// loaded once per live connection and address/pin, before the first query.
func ModuleGraphQLClient(client *Client, address, pin string) graphql.Client {
	return moduleGraphQLClient{client: client, spec: moduleSpec{address, pin}}
}

type moduleGraphQLClient struct {
	client *Client
	spec   moduleSpec
}

func (g moduleGraphQLClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	client := g.client
	if client == nil {
		var err error
		client, err = Default(ctx)
		if err != nil {
			return err
		}
	}
	if err := client.ensureModule(ctx, g.spec); err != nil {
		return err
	}
	err := client.GraphQLClient().MakeRequest(ctx, req, resp)
	if missingGraphQLField(err) {
		return fmt.Errorf("generated client for %q is out of date; regenerate it: %w", g.spec.address, err)
	}
	return err
}

func (c *Client) ensureModule(ctx context.Context, spec moduleSpec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.modulesMu.Lock()
	if c.closed {
		c.modulesMu.Unlock()
		return ErrClientClosed
	}
	if load := c.modules[spec]; load != nil {
		c.modulesMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-load.done:
			return load.err
		}
	}
	if c.modules == nil {
		c.modules = make(map[moduleSpec]*moduleLoad)
	}
	load := &moduleLoad{done: make(chan struct{})}
	c.modules[spec] = load
	c.modulesMu.Unlock()

	req := &graphql.Request{
		Query:  `query LoadModule($address: String!, $pin: String) { serveModule(address: $address, refPin: $pin) }`,
		OpName: "LoadModule", Variables: map[string]any{"address": spec.address, "pin": spec.pin},
	}
	err := c.GraphQLClient().MakeRequest(ctx, req, &graphql.Response{})
	if err != nil {
		if missingGraphQLField(err) {
			err = fmt.Errorf("the engine does not support generated module clients (serveModule is required): %w", err)
		} else {
			err = fmt.Errorf("load module %q: %w", spec.address, err)
		}
	}
	c.modulesMu.Lock()
	load.err = err
	if err != nil {
		delete(c.modules, spec)
	}
	close(load.done)
	c.modulesMu.Unlock()
	return err
}

func missingGraphQLField(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) {
		for _, item := range httpErr.Response.Errors {
			if missingFieldError(item) {
				return true
			}
		}
	}
	var list gqlerror.List
	if errors.As(err, &list) {
		for _, item := range list {
			if missingFieldError(item) {
				return true
			}
		}
	}
	var item *gqlerror.Error
	return errors.As(err, &item) && missingFieldError(item)
}

func missingFieldError(err *gqlerror.Error) bool {
	return err != nil && len(err.Path) == 0 && err.Extensions["code"] == "GRAPHQL_VALIDATION_FAILED" && strings.HasPrefix(err.Message, "Cannot query field ")
}

func (c *Client) isClosed() bool {
	c.modulesMu.Lock()
	defer c.modulesMu.Unlock()
	return c.closed
}
