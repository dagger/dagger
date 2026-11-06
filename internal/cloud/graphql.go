package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type graphQLError struct {
	Message string `json:"message"`
}

func (e graphQLError) String() string {
	return e.Message
}

// GraphQLHTTPError is a GraphQL request that Cloud answered with a non-2xx
// HTTP status, so callers can tell, e.g., rejected credentials apart.
type GraphQLHTTPError struct {
	Op         string
	Status     string
	StatusCode int
	Body       string
}

func (e *GraphQLHTTPError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Op, e.Status, e.Body)
}

// GraphQLErrors are the errors of a GraphQL response: messages Cloud's API
// wrote for its clients.
type GraphQLErrors struct {
	Op       string
	Messages []string
}

func (e *GraphQLErrors) Error() string {
	return fmt.Sprintf("%s: %s", e.Op, strings.Join(e.Messages, "; "))
}

func (c *Client) doGraphQL(ctx context.Context, opName string, query string, variables map[string]any, out any) error {
	return c.doGraphQLWithHeader(ctx, opName, query, variables, nil, out)
}

// doGraphQLWithHeader is doGraphQL with extra request headers, which override
// the defaults.
func (c *Client) doGraphQLWithHeader(ctx context.Context, opName string, query string, variables map[string]any, header http.Header, out any) error {
	body, err := json.Marshal(&graphqlRequest{
		OpName:    opName,
		Query:     query,
		Variables: variables,
	})
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", opName, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.u.JoinPath("/query").String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", opName, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json")
	for key, values := range header {
		req.Header[key] = values
	}

	resp, err := c.h.Do(req)
	if err != nil {
		return fmt.Errorf("send %s request: %w", opName, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", opName, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &GraphQLHTTPError{
			Op:         opName,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(respBody)),
		}
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", opName, err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, gqlErr := range envelope.Errors {
			msgs = append(msgs, gqlErr.Message)
		}
		return &GraphQLErrors{Op: opName, Messages: msgs}
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode %s data: %w", opName, err)
	}
	return nil
}
