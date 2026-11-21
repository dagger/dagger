package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
)

// ErrLiveSpanQueryUnsupported reports an engine that serves no span queries
// (enginetel.LiveFeatureSpanQuery).
var ErrLiveSpanQueryUnsupported = errors.New("engine does not serve live span queries")

// QuerySpans reads the newest snapshots of the spans q selects from this
// client's own store: one-shot, as of the request. consume receives each
// batch, its spans stamped with their child counts and whether they have
// display text; it returns the store's span high-water mark the answer
// reflects.
func (c *Client) QuerySpans(ctx context.Context, q clientdb.SpanQuery, consume func(*coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	if !c.SupportsTelemetryFeature(enginetel.LiveFeatureSpanQuery) {
		return 0, ErrLiveSpanQueryUnsupported
	}
	body, err := json.Marshal(q)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dagger"+enginetel.LiveSpanQueryPath, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp, err := c.liveTelemetryHTTP().Do(req)
	if err != nil {
		return 0, fmt.Errorf("query spans: %w", err)
	}
	defer resp.Body.Close()
	if err := expectLiveLogResponse(resp, enginetel.LiveContentType); err != nil {
		return 0, err
	}
	return consumeSpanQuery(resp.Body, consume)
}

func consumeSpanQuery(r io.Reader, consume func(*coltracepb.ExportTraceServiceRequest) error) (int64, error) {
	for {
		kind, cursor, payload, err := enginetel.ReadLiveFrame(r)
		if err != nil {
			return 0, fmt.Errorf("read span query: %w", err)
		}
		switch kind {
		case enginetel.LiveFrameHello:
			continue
		case enginetel.LiveFrameTerminal:
			return cursor, nil
		}
		batch := &coltracepb.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return 0, fmt.Errorf("decode span query batch: %w", err)
		}
		if err := consume(batch); err != nil {
			return 0, err
		}
	}
}
