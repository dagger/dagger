package daggertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
)

type Conn struct {
	Stubs []QueryStub
}

type QueryStub struct {
	Query  string
	Result []byte
}

type EngineConn interface {
	graphql.Doer
	Host() string
	Close() error
}

var _ EngineConn = (*Conn)(nil)

func (c *Conn) Stub(sel *querybuilder.Selection, data any) {
	query, err := sel.Build(context.TODO())
	if err != nil {
		panic(err)
	}
	payload, err := json.Marshal(graphql.Response{
		Data: sel.Pack(data),
	})
	if err != nil {
		panic(err)
	}
	c.Stubs = append(c.Stubs, QueryStub{
		Query:  query,
		Result: payload,
	})
}

func (c *Conn) Do(req *http.Request) (*http.Response, error) {
	var body graphql.Request
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return nil, err
	}
	for _, stub := range c.Stubs {
		if stub.Query == body.Query {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBuffer(stub.Result)),
			}, nil
		}
	}
	return nil, fmt.Errorf("no stub found for query: %s", body.Query)
}

func (c *Conn) Host() string {
	return "test"
}
func (c *Conn) Close() error {
	return nil
}
