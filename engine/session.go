package engine

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	SessionsEndpoint = "/v1/sessions"

	SessionIDPrefix    = "sess_"
	QueryIDPrefix      = "qry_"
	AttachmentIDPrefix = "att_"

	SessionIDEncodedLength = 26
)

type SessionState string

const (
	SessionStateAttached    SessionState = "attached"
	SessionStateDetached    SessionState = "detached"
	SessionStateTerminating SessionState = "terminating"
)

type SessionQueryState string

const (
	SessionQueryStateRunning         SessionQueryState = "running"
	SessionQueryStateSucceeded       SessionQueryState = "succeeded"
	SessionQueryStateFailed          SessionQueryState = "failed"
	SessionQueryStateCanceled        SessionQueryState = "canceled"
	SessionQueryStateResultDiscarded SessionQueryState = "result_discarded"
)

type SessionAttachment struct {
	ID         string    `json:"id"`
	Generation uint64    `json:"generation"`
	ClientID   string    `json:"client_id"`
	Hostname   string    `json:"hostname,omitempty"`
	Ready      bool      `json:"ready"`
	CreatedAt  time.Time `json:"created_at"`
}

type SessionServiceKey struct {
	Digest      string `json:"digest"`
	SessionID   string `json:"session_id"`
	ClientID    string `json:"client_id,omitempty"`
	RuntimeKind string `json:"runtime_kind"`
	InstanceID  string `json:"instance_id,omitempty"`
}

type SessionPort struct {
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Description string `json:"description,omitempty"`
}

type SessionService struct {
	Key                 SessionServiceKey  `json:"key"`
	Names               []string           `json:"names"`
	Kind                string             `json:"kind"`
	Host                string             `json:"host"`
	Ports               []SessionPort      `json:"ports"`
	OwnerClientID       string             `json:"owner_client_id,omitempty"`
	OwnerClientHostname string             `json:"owner_client_hostname,omitempty"`
	TunnelUpstream      *SessionServiceKey `json:"tunnel_upstream,omitempty"`
	Retained            bool               `json:"retained"`
}

type QueryPresentation struct {
	Kind         string   `json:"kind"`
	ResponsePath []string `json:"response_path"`
	ReturnKind   string   `json:"return_kind"`
	ElementKind  string   `json:"element_kind,omitempty"`
	JSON         bool     `json:"json"`
	Void         bool     `json:"void"`
}

type SessionQuery struct {
	ID              string                `json:"id"`
	Status          SessionQueryState     `json:"status"`
	CreatedAt       time.Time             `json:"created_at"`
	StartedAt       time.Time             `json:"started_at,omitempty"`
	CompletedAt     time.Time             `json:"completed_at,omitempty"`
	ResultAvailable bool                  `json:"result_available"`
	Presentation    QueryPresentation     `json:"presentation"`
	Error           string                `json:"error,omitempty"`
	Telemetry       SessionTelemetryBound `json:"telemetry"`
}

type SessionTelemetryBound struct {
	Traces  int64 `json:"traces"`
	Logs    int64 `json:"logs"`
	Metrics int64 `json:"metrics"`
}

type SessionDescriptor struct {
	ID         string             `json:"id"`
	State      SessionState       `json:"state"`
	CreatedAt  time.Time          `json:"created_at"`
	DetachedAt time.Time          `json:"detached_at,omitempty"`
	Attachment *SessionAttachment `json:"attachment,omitempty"`
	Query      *SessionQuery      `json:"query,omitempty"`
	Services   []SessionService   `json:"services"`
}

type SubmitPrimaryQueryRequest struct {
	AttachmentID string            `json:"attachment_id"`
	Request      json.RawMessage   `json:"request"`
	Presentation QueryPresentation `json:"presentation"`
}

type SubmitPrimaryQueryResponse struct {
	SessionID string            `json:"session_id"`
	QueryID   string            `json:"query_id"`
	Status    SessionQueryState `json:"status"`
}

type SessionResultMetadata struct {
	StatusCode  int                 `json:"status_code"`
	Header      map[string][]string `json:"header,omitempty"`
	CompletedAt time.Time           `json:"completed_at"`
	Discarded   bool                `json:"discarded"`
	Error       string              `json:"error,omitempty"`
}

type SessionProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SessionProtocolErrorResponse struct {
	Error SessionProtocolError `json:"error"`
}

const (
	SessionErrorMalformedID                = "malformed_id"
	SessionErrorInvalidRequest             = "invalid_request"
	SessionErrorRequestTooLarge            = "request_too_large"
	SessionErrorSessionNotFound            = "session_not_found"
	SessionErrorSessionTerminated          = "session_terminated"
	SessionErrorSessionTerminating         = "session_terminating"
	SessionErrorAlreadyAttached            = "already_attached"
	SessionErrorAttachmentConnectionExists = "attachment_connection_exists"
	SessionErrorAttachmentMismatch         = "attachment_mismatch"
	SessionErrorPrimaryQueryExists         = "primary_query_exists"
	SessionErrorPrimaryQueryNotFound       = "primary_query_not_found"
	SessionErrorQueryRunning               = "query_running"
	SessionErrorResultDiscarded            = "result_discarded"
	SessionErrorTelemetryUnavailable       = "telemetry_unavailable"
)

func GenerateSessionID() (string, error) {
	return generateSessionProtocolID(SessionIDPrefix)
}

func GenerateQueryID() (string, error) {
	return generateSessionProtocolID(QueryIDPrefix)
}

func GenerateAttachmentID() (string, error) {
	return generateSessionProtocolID(AttachmentIDPrefix)
}

func ValidateSessionID(id string) error {
	return validateSessionProtocolID(id, SessionIDPrefix)
}

func ValidateQueryID(id string) error {
	return validateSessionProtocolID(id, QueryIDPrefix)
}

func ValidateAttachmentID(id string) error {
	return validateSessionProtocolID(id, AttachmentIDPrefix)
}

func generateSessionProtocolID(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", strings.TrimSuffix(prefix, "_"), err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return prefix + strings.ToLower(encoded), nil
}

func validateSessionProtocolID(id, prefix string) error {
	if len(id) != len(prefix)+SessionIDEncodedLength || !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("identifier must be %s followed by %d lowercase base32 characters", prefix, SessionIDEncodedLength)
	}
	for _, ch := range id[len(prefix):] {
		if (ch < 'a' || ch > 'z') && (ch < '2' || ch > '7') {
			return fmt.Errorf("identifier must be %s followed by %d lowercase base32 characters", prefix, SessionIDEncodedLength)
		}
	}
	return nil
}
