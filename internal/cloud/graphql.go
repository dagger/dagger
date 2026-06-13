package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type graphQLErrors []graphQLError

func (errs graphQLErrors) Error() string {
	msgs := make([]string, 0, len(errs))
	for _, gqlErr := range errs {
		msgs = append(msgs, gqlErr.Message)
	}
	return strings.Join(msgs, "; ")
}

// IsGraphQLFieldUnavailable reports whether err is Cloud GraphQL telling us a
// field is not present in the deployed schema. This lets clients ship before a
// new Cloud field is everywhere and keep an older fallback path.
func IsGraphQLFieldUnavailable(err error, field string) bool {
	var gqlErrs graphQLErrors
	if !errors.As(err, &gqlErrs) {
		return false
	}
	needle := fmt.Sprintf("Cannot query field %q", field)
	for _, gqlErr := range gqlErrs {
		if strings.Contains(gqlErr.Message, needle) {
			return true
		}
	}
	return false
}

func IsCheckReportUnavailable(err error) bool {
	if IsGraphQLFieldUnavailable(err, "report") || IsGraphQLFieldUnavailable(err, "check") {
		return true
	}
	var gqlErrs graphQLErrors
	if !errors.As(err, &gqlErrs) {
		return false
	}
	for _, gqlErr := range gqlErrs {
		if strings.Contains(gqlErr.Message, "CheckReportInput") ||
			strings.Contains(gqlErr.Message, "CheckReport") {
			return true
		}
	}
	return false
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
		return fmt.Errorf("%s: %w", opName, graphQLErrors(envelope.Errors))
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
