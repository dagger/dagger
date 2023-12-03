package daggertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"dagger.io/dagger/internal/engineconn"
	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

// Conn is a stubbable EngineConn.
type Conn struct {
	t     testing.TB
	stubs map[string]graphql.Response
}

var _ engineconn.EngineConn = (*Conn)(nil)

// NewConn returns a new Conn.
func NewConn(t testing.TB) *Conn {
	return &Conn{
		t:     t,
		stubs: make(map[string]graphql.Response),
	}
}

// Stub adds a stubbed response for the given query.
func (c *Conn) Stub(q *querybuilder.Selection, data any) {
	query, err := q.Build(context.TODO())
	require.NoError(c.t, err)
	c.stubs[query] = graphql.Response{
		Data: q.Pack(data),
	}
}

// Do implements graphql.Doer.
//
// It returns a stubbed response if one is available for the given query.
// Otherwise, it returns an error.
//
// The usual workflow is to just copy-paste the query from the error message
// and use it to write a new stub.
func (c *Conn) Do(req *http.Request) (*http.Response, error) {
	var body graphql.Request
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	data, found := c.stubs[body.Query]
	if !found {
		return nil, fmt.Errorf("no stub found for query: %s", body.Query)
	}
	payload, err := json.Marshal(data)
	require.NoError(c.t, err)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBuffer(payload)),
	}, nil
}

// Host implements EngineConn.
func (c *Conn) Host() string {
	return "test"
}

// Host implements EngineConn.
func (c *Conn) Close() error {
	return nil
}
