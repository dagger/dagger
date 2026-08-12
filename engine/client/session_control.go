package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine"
)

type SessionProtocolError struct {
	StatusCode int
	Code       string
	Message    string
}

var ErrDetachableSessionsUnsupported = errors.New("engine does not support detachable sessions; upgrade the engine")

func (err *SessionProtocolError) Error() string {
	if err.Code == "" {
		return fmt.Sprintf("session protocol returned HTTP %d: %s", err.StatusCode, err.Message)
	}
	return fmt.Sprintf("session protocol %s (HTTP %d): %s", err.Code, err.StatusCode, err.Message)
}

type attachmentRetryRole uint8

const (
	attachmentRetryCreator attachmentRetryRole = iota
	attachmentRetryObserver
)

type attachmentRetryPolicy struct {
	timeout        time.Duration
	initialBackoff time.Duration
	maxBackoff     time.Duration
	now            func() time.Time
	wait           func(context.Context, time.Duration) error
}

func defaultAttachmentRetryPolicy() attachmentRetryPolicy {
	return attachmentRetryPolicy{
		timeout: 30 * time.Second, initialBackoff: 100 * time.Millisecond,
		maxBackoff: 2 * time.Second, now: time.Now,
		wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		},
	}
}

func retrySessionAttachment(
	ctx context.Context,
	role attachmentRetryRole,
	onWait func(),
	attempt func(context.Context) error,
	policy attachmentRetryPolicy,
) error {
	if policy.now == nil || policy.wait == nil || policy.timeout <= 0 {
		return errors.New("invalid attachment retry policy")
	}
	deadline := policy.now().Add(policy.timeout)
	retryCtx, cancel := context.WithDeadlineCause(ctx, deadline, context.DeadlineExceeded)
	defer cancel()
	backoff := policy.initialBackoff
	notified := false
	for {
		err := attempt(retryCtx)
		if err == nil {
			return nil
		}
		var protocolErr *SessionProtocolError
		if !errors.As(err, &protocolErr) || protocolErr.StatusCode != http.StatusConflict {
			return err
		}
		retryCode := engine.SessionErrorAttachmentConnectionExists
		if role == attachmentRetryObserver {
			retryCode = engine.SessionErrorAlreadyAttached
		}
		if protocolErr.Code != retryCode {
			return err
		}
		if role == attachmentRetryObserver && !notified {
			notified = true
			if onWait != nil {
				onWait()
			}
		}
		if !policy.now().Before(deadline) {
			return fmt.Errorf("attachment retry deadline exceeded: %w", err)
		}
		if backoff <= 0 {
			backoff = time.Millisecond
		}
		if remaining := deadline.Sub(policy.now()); backoff > remaining {
			backoff = remaining
		}
		if waitErr := policy.wait(retryCtx, backoff); waitErr != nil {
			return fmt.Errorf("attachment retry deadline exceeded: %w", err)
		}
		backoff *= 2
		if backoff > policy.maxBackoff {
			backoff = policy.maxBackoff
		}
	}
}

type ControlClient struct {
	client *Client
}

func ConnectControl(ctx context.Context, params Params) (*ControlClient, error) {
	params.Detachable = false
	params.AttachSessionID = ""
	client, err := connect(ctx, params, connectionModeControl)
	if err != nil {
		return nil, err
	}
	return &ControlClient{client: client}, nil
}

func (client *ControlClient) Close() error {
	if client == nil || client.client == nil {
		return nil
	}
	return client.client.Close()
}

func (client *ControlClient) ListSessions(ctx context.Context) ([]engine.SessionDescriptor, error) {
	var sessions []engine.SessionDescriptor
	if err := client.client.sessionJSON(ctx, http.MethodGet, engine.SessionsEndpoint, nil, http.StatusOK, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (client *ControlClient) InspectSession(ctx context.Context, sessionID string) (engine.SessionDescriptor, error) {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return engine.SessionDescriptor{}, err
	}
	var descriptor engine.SessionDescriptor
	path := engine.SessionsEndpoint + "/" + url.PathEscape(sessionID)
	if err := client.client.sessionJSON(ctx, http.MethodGet, path, nil, http.StatusOK, &descriptor); err != nil {
		return engine.SessionDescriptor{}, err
	}
	return descriptor, nil
}

func (client *ControlClient) TerminateSession(ctx context.Context, sessionID string) error {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return err
	}
	path := engine.SessionsEndpoint + "/" + url.PathEscape(sessionID)
	return client.client.sessionJSON(ctx, http.MethodDelete, path, nil, http.StatusNoContent, nil)
}

func (client *ControlClient) InspectPrimaryQuery(ctx context.Context, sessionID string) (engine.SessionQuery, error) {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return engine.SessionQuery{}, err
	}
	return client.client.inspectPrimaryQuery(ctx, sessionID)
}

func (client *ControlClient) PrimaryQueryResult(ctx context.Context, sessionID string) (SessionResult, error) {
	if err := engine.ValidateSessionID(sessionID); err != nil {
		return SessionResult{}, err
	}
	return client.client.primaryQueryResult(ctx, sessionID)
}

func (c *Client) AttachmentID() string {
	return c.attachmentID
}

func (c *Client) InspectSession(ctx context.Context) (engine.SessionDescriptor, error) {
	var descriptor engine.SessionDescriptor
	path := engine.SessionsEndpoint + "/" + url.PathEscape(c.SessionID)
	if err := c.sessionJSON(ctx, http.MethodGet, path, nil, http.StatusOK, &descriptor); err != nil {
		return engine.SessionDescriptor{}, err
	}
	return descriptor, nil
}

func (c *Client) CloseAttachment(ctx context.Context) error {
	if c.attachmentID == "" {
		return nil
	}
	path := engine.SessionsEndpoint + "/" + url.PathEscape(c.SessionID) +
		"/attachments/" + url.PathEscape(c.attachmentID)
	err := c.sessionJSON(ctx, http.MethodDelete, path, nil, http.StatusNoContent, nil)
	var protocolErr *SessionProtocolError
	if errors.As(err, &protocolErr) && protocolErr.Code == engine.SessionErrorSessionNotFound {
		c.attachmentID = ""
		return nil
	}
	if err == nil {
		c.attachmentID = ""
	}
	return err
}

func (c *Client) Terminate(ctx context.Context) error {
	err := c.terminateSessionRemote(ctx)
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("termination started but completion was not observed: %w", err)
	}
	c.attachmentID = ""
	return errors.Join(err, c.closeLocalResources())
}

func (c *Client) terminateSessionRemote(ctx context.Context) error {
	path := engine.SessionsEndpoint + "/" + url.PathEscape(c.SessionID)
	return c.sessionJSON(ctx, http.MethodDelete, path, nil, http.StatusNoContent, nil)
}

func (c *Client) SubmitPrimaryQuery(ctx context.Context, request json.RawMessage, presentation engine.QueryPresentation) (engine.SubmitPrimaryQueryResponse, error) {
	payload := engine.SubmitPrimaryQueryRequest{
		AttachmentID: c.attachmentID, Request: request, Presentation: presentation,
	}
	var response engine.SubmitPrimaryQueryResponse
	path := engine.SessionsEndpoint + "/" + url.PathEscape(c.SessionID) + "/queries/primary"
	if err := c.sessionJSON(ctx, http.MethodPost, path, payload, http.StatusAccepted, &response); err != nil {
		return engine.SubmitPrimaryQueryResponse{}, err
	}
	c.detachedQueryAcknowledged.Store(true)
	return response, nil
}

func (c *Client) DetachedQueryAcknowledged() bool {
	return c.detachedQueryAcknowledged.Load()
}

func (c *Client) InspectPrimaryQuery(ctx context.Context) (engine.SessionQuery, error) {
	return c.inspectPrimaryQuery(ctx, c.SessionID)
}

func (c *Client) inspectPrimaryQuery(ctx context.Context, sessionID string) (engine.SessionQuery, error) {
	var query engine.SessionQuery
	path := engine.SessionsEndpoint + "/" + url.PathEscape(sessionID) + "/queries/primary"
	if err := c.sessionJSON(ctx, http.MethodGet, path, nil, http.StatusOK, &query); err != nil {
		return engine.SessionQuery{}, err
	}
	return query, nil
}

type SessionResult struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (c *Client) PrimaryQueryResult(ctx context.Context) (SessionResult, error) {
	return c.primaryQueryResult(ctx, c.SessionID)
}

func (c *Client) primaryQueryResult(ctx context.Context, sessionID string) (SessionResult, error) {
	path := engine.SessionsEndpoint + "/" + url.PathEscape(sessionID) + "/queries/primary/result"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://dagger"+path, nil)
	if err != nil {
		return SessionResult{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SessionResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SessionResult{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		var protocolResponse engine.SessionProtocolErrorResponse
		if json.Unmarshal(body, &protocolResponse) == nil && protocolResponse.Error.Code != "" {
			return SessionResult{}, &SessionProtocolError{
				StatusCode: resp.StatusCode,
				Code:       protocolResponse.Error.Code,
				Message:    protocolResponse.Error.Message,
			}
		}
	}
	return SessionResult{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), Body: body}, nil
}

func (c *Client) WaitTelemetry() error {
	if c.telemetry == nil {
		return nil
	}
	return c.telemetry.Wait()
}

func (c *Client) SessionTelemetryPath(signal string) string {
	return engine.SessionsEndpoint + "/" + url.PathEscape(c.SessionID) + "/telemetry/" + signal
}

func (c *Client) telemetryPath(signal string) string {
	if c.connectionMode == connectionModeObserver {
		return c.SessionTelemetryPath(signal)
	}
	return "/v1/" + signal
}

func (c *Client) telemetryConsumerContext(ctx context.Context) context.Context {
	ctx = context.WithoutCancel(ctx)
	if c.connectionMode != connectionModeDetachableCreator && c.connectionMode != connectionModeObserver {
		return ctx
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-c.closeCtx.Done():
			cancel(context.Cause(c.closeCtx))
		case <-ctx.Done():
		}
	}()
	return ctx
}

func (c *Client) sessionJSON(ctx context.Context, method, path string, input any, expectedStatus int, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://dagger"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != expectedStatus {
		err := decodeSessionProtocolError(resp.StatusCode, responseBody)
		if isIncompatibleSessionProtocolError(err) {
			return fmt.Errorf("%w: %w", ErrDetachableSessionsUnsupported, err)
		}
		return err
	}
	if output != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, output); err != nil {
			return fmt.Errorf("decode session response: %w", err)
		}
	}
	return nil
}

func isIncompatibleSessionProtocolError(err error) bool {
	var protocolErr *SessionProtocolError
	return errors.As(err, &protocolErr) &&
		protocolErr.StatusCode == http.StatusNotFound && protocolErr.Code == ""
}

func decodeSessionProtocolError(statusCode int, body []byte) error {
	var response engine.SessionProtocolErrorResponse
	if err := json.Unmarshal(body, &response); err == nil && response.Error.Message != "" {
		return &SessionProtocolError{
			StatusCode: statusCode, Code: response.Error.Code, Message: response.Error.Message,
		}
	}
	return &SessionProtocolError{
		StatusCode: statusCode, Message: string(bytes.TrimSpace(body)),
	}
}
