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

func (c *Client) doGraphQL(ctx context.Context, opName string, query string, variables map[string]any, out any) error {
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
		return fmt.Errorf("%s: %s: %s", opName, resp.Status, strings.TrimSpace(string(respBody)))
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
		return fmt.Errorf("%s: %s", opName, strings.Join(msgs, "; "))
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
