package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

// Call closures. A frontend that loaded only part of a trace (a
// conversation-only restore) lacks most call payloads, which ride the whole
// log stream or the spans it skipped. Rebuilding an ID -- to branch from a
// message a rewind abandoned, open a terminal on an old container, or address
// an agent -- needs every frame of the call's recipe, so it asks for the
// recipe closure of the digests it holds.

// CallClosureResource is the archive endpoint that reads the call payloads of
// recipe closures (POST a CallClosureQuery). The reply is a framed log stream
// of call-payload records whose cursors count its frames, not archive rows.
const CallClosureResource = "calls"

// MaxCallClosureDigests bounds the digests one call-closure query names.
const MaxCallClosureDigests = 256

// CallClosureQuery names the calls whose recipe closures to read.
type CallClosureQuery struct {
	Digests []string `json:"digests"`
	// Known names calls whose recipe closures the caller already holds: the
	// read neither sends them nor walks into them. E.g. a rewind's adopted
	// state, which bounds the abandoned conversation the caller wants from
	// the prefix both share.
	Known []string `json:"known,omitempty"`
}

// Validate checks q against the shared client/server contract.
func (q CallClosureQuery) Validate() error {
	if len(q.Digests) == 0 {
		return errors.New("call closure query names no calls")
	}
	if len(q.Digests) > MaxCallClosureDigests {
		return fmt.Errorf("call closure query names %d calls, more than %d", len(q.Digests), MaxCallClosureDigests)
	}
	if len(q.Known) > MaxCallClosureDigests {
		return fmt.Errorf("call closure query names %d known calls, more than %d", len(q.Known), MaxCallClosureDigests)
	}
	for _, d := range append(slices.Clone(q.Digests), q.Known...) {
		if d == "" {
			return errors.New("call closure query names an empty digest")
		}
	}
	return nil
}

// ErrCallClosureUnsupported fails a call-closure read on an engine that
// predates the endpoint.
var ErrCallClosureUnsupported = errors.New("engine archive cannot read call closures")

// CallClosure reads the call payloads of digests' recipe closures, up to the
// archive's cut, as call-payload log records: every frame the archive holds,
// each once, in no particular order. It is best-effort: a frame the archive
// does not hold is left out, along with what only it references, so a caller
// still checks that the rebuild it wanted closes. The closures of known calls
// are left out (CallClosureQuery.Known). unsealed reads an interrupted
// archive, as StreamOptions.Unsealed.
func (c *Client) CallClosure(ctx context.Context, traceID string, unsealed bool, digests, known []string, consume func(*collogspb.ExportLogsServiceRequest) error) error {
	q := CallClosureQuery{Digests: digests, Known: known}
	if err := q.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return err
	}
	query := url.Values{}
	if unsealed {
		query.Set("unsealed", "1")
	}
	resp, err := c.do(ctx, http.MethodPost, archiveResourcePath(traceID, CallClosureResource), query, body, enginetel.LiveContentType, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed {
		// An engine that predates the endpoint refuses a POST to an
		// archive resource it does not know.
		return ErrCallClosureUnsupported
	}
	if err := expectOK(resp); err != nil {
		return err
	}
	if err := expectContentType(resp, enginetel.LiveContentType); err != nil {
		return corrupt(err)
	}
	var cursor int64
	for {
		kind, next, payload, err := enginetel.ReadLiveFrame(resp.Body)
		if err != nil {
			if errors.Is(err, enginetel.ErrInvalidLiveFrame) || errors.Is(err, enginetel.ErrLiveStream) {
				return corrupt(err)
			}
			return transient(fmt.Errorf("read archive call closure: %w", err))
		}
		switch kind {
		case enginetel.LiveFrameHello:
			continue
		case enginetel.LiveFrameTerminal:
			if next != cursor {
				return corrupt(fmt.Errorf("archive call closure ended at frame %d after frame %d", next, cursor))
			}
			return nil
		}
		if next != cursor+1 {
			return corrupt(fmt.Errorf("archive call closure frame %d follows frame %d", next, cursor))
		}
		cursor = next
		batch := &collogspb.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(payload, batch); err != nil {
			return corrupt(fmt.Errorf("decode archive call closure: %w", err))
		}
		if consume != nil {
			if err := consume(batch); err != nil {
				return err
			}
		}
	}
}
