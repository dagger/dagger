package cloudpull

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	cloud "github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/dagger/dagger/internal/odag/store"
)

type PullOptions struct {
	OrgName    string
	Timeout    time.Duration
	SourceMode string
}

type PullResult struct {
	TraceID       string `json:"traceID"`
	Batches       int    `json:"batches"`
	SpanUpdates   int    `json:"spanUpdates"`
	TracesTouched int    `json:"tracesTouched"`
}

func PullTrace(ctx context.Context, st *store.Store, traceID string, opts PullOptions) (PullResult, error) {
	if st == nil {
		return PullResult{}, fmt.Errorf("store is required")
	}
	if traceID == "" {
		return PullResult{}, fmt.Errorf("trace id is required")
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return PullResult{}, fmt.Errorf("cloud auth: %w", err)
	}
	if cloudAuth == nil || cloudAuth.Token == nil {
		return PullResult{}, fmt.Errorf("not authenticated; run 'dagger login' or set DAGGER_CLOUD_TOKEN")
	}

	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		return PullResult{}, fmt.Errorf("cloud client: %w", err)
	}

	orgID, err := resolveOrgID(ctx, client, cloudAuth, opts.OrgName)
	if err != nil {
		return PullResult{}, err
	}

	sourceMode := opts.SourceMode
	if sourceMode == "" {
		sourceMode = "cloud"
	}

	var res PullResult
	res.TraceID = traceID

	var callbackErr error
	err = client.StreamSpans(ctx, orgID, traceID, func(spanDatas []cloud.SpanData) {
		if callbackErr != nil {
			return
		}

		recs, convErr := spansToRecords(spanDatas)
		if convErr != nil {
			callbackErr = convErr
			return
		}

		summary, ingestErr := st.UpsertSpans(ctx, sourceMode, recs)
		if ingestErr != nil {
			callbackErr = ingestErr
			return
		}

		res.Batches++
		res.SpanUpdates += summary.Spans
		res.TracesTouched += summary.Traces
	})
	if callbackErr != nil {
		return PullResult{}, callbackErr
	}
	if err != nil {
		return PullResult{}, fmt.Errorf("stream spans: %w", err)
	}

	return res, nil
}

func resolveOrgID(ctx context.Context, client *cloud.Client, cloudAuth *auth.Cloud, orgName string) (string, error) {
	if orgName != "" {
		org, err := client.OrgByName(ctx, orgName)
		if err != nil {
			return "", fmt.Errorf("resolve org %q: %w", orgName, err)
		}
		return org.ID, nil
	}

	if cloudAuth != nil && cloudAuth.Org != nil && cloudAuth.Org.ID != "" {
		return cloudAuth.Org.ID, nil
	}

	return "", fmt.Errorf("no org specified; use --org or run 'dagger login' to set a default org")
}

func spansToRecords(spans []cloud.SpanData) ([]store.SpanRecord, error) {
	if len(spans) == 0 {
		return nil, nil
	}

	out := make([]store.SpanRecord, 0, len(spans))
	for _, span := range spans {
		data := map[string]any{}
		if len(span.Attributes) > 0 {
			data["attributes"] = span.Attributes
		}
		if len(span.Events) > 0 {
			data["events"] = spanEventsToJSON(span.Events)
		}
		if len(span.Links) > 0 {
			data["links"] = spanLinksToJSON(span.Links)
		}
		if span.Scope.Name != "" || span.Scope.Version != "" {
			data["scope"] = map[string]any{
				"name":    span.Scope.Name,
				"version": span.Scope.Version,
			}
		}

		payload, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("marshal span payload (%s): %w", span.ID, err)
		}

		parentSpanID := ""
		if span.ParentID != nil {
			parentSpanID = *span.ParentID
		}

		endUnixNano := int64(0)
		if span.EndTime != nil {
			endUnixNano = span.EndTime.UnixNano()
		}

		out = append(out, store.SpanRecord{
			TraceID:         span.TraceID,
			SpanID:          span.ID,
			ParentSpanID:    parentSpanID,
			Name:            span.Name,
			StartUnixNano:   span.Timestamp.UnixNano(),
			EndUnixNano:     endUnixNano,
			StatusCode:      span.Status.Code,
			StatusMessage:   span.Status.Message,
			DataJSON:        string(payload),
			UpdatedUnixNano: span.UpdateTime.UnixNano(),
		})
	}
	return out, nil
}

func spanEventsToJSON(events []cloud.SpanEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		ev := map[string]any{
			"time_unix_nano": event.Timestamp.UnixNano(),
			"name":           event.Name,
		}
		if len(event.Attributes) > 0 {
			ev["attributes"] = event.Attributes
		}
		out = append(out, ev)
	}
	return out
}

func spanLinksToJSON(links []cloud.SpanLink) []map[string]any {
	out := make([]map[string]any, 0, len(links))
	for _, link := range links {
		item := map[string]any{
			"trace_id": link.TraceID,
			"span_id":  link.SpanID,
		}
		if len(link.Attributes) > 0 {
			item["attributes"] = link.Attributes
		}
		out = append(out, item)
	}
	return out
}
