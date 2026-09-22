package archive

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/protobuf/proto"
)

// Completion is an independently supplied roster/revision witness, not a
// second lifecycle projection. The actual facts remain typed OTLP records.
type Completion struct {
	Agents        []AgentRevision        `json:"agents"`
	Subscriptions []SubscriptionRevision `json:"subscriptions"`
}
type AgentRevision struct {
	Key      agentcontrol.Key
	Revision int64
}
type SubscriptionRevision struct {
	Key      agentcontrol.EdgeKey
	Revision int64
}

func Witness(want agentcontrol.Expectation) Completion {
	out := Completion{}
	for k, r := range want.Agents {
		out.Agents = append(out.Agents, AgentRevision{k, r})
	}
	for k, r := range want.Subscriptions {
		out.Subscriptions = append(out.Subscriptions, SubscriptionRevision{k, r})
	}
	return out
}
func (w Completion) Expectation() (agentcontrol.Expectation, error) {
	out := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{}, Subscriptions: map[agentcontrol.EdgeKey]int64{}}
	for _, a := range w.Agents {
		if _, ok := out.Agents[a.Key]; ok {
			return out, errors.New("duplicate agent witness")
		}
		out.Agents[a.Key] = a.Revision
	}
	for _, s := range w.Subscriptions {
		if _, ok := out.Subscriptions[s.Key]; ok {
			return out, errors.New("duplicate subscription witness")
		}
		out.Subscriptions[s.Key] = s.Revision
	}
	return out, nil
}

// VerifyClosure loads and validates raw recipes without evaluating any recipe.
// The returned payloads are exactly the dependency closure of the anchors.
func VerifyClosure(roots []string, load func(string) (*callpbv1.Call, error)) (map[string]*callpbv1.Call, error) {
	calls := map[string]*callpbv1.Call{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(d string) error {
		if visiting[d] {
			return fmt.Errorf("cyclic call recipe %s", d)
		}
		if calls[d] != nil {
			return nil
		}
		c, err := load(d)
		if err != nil {
			return err
		}
		if c == nil || c.Digest != d {
			return fmt.Errorf("call digest mismatch %s", d)
		}
		if c.Type == nil {
			return fmt.Errorf("missing call type %s", d)
		}
		visiting[d] = true
		refs, err := call.RecipeReferences(c)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := visit(ref); err != nil {
				return err
			}
		}
		delete(visiting, d)
		calls[d] = c
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	if len(roots) > 0 {
		if err := call.ValidateRecipeDAG(&callpbv1.RecipeDAG{RootDigest: roots[0], CallsByDigest: calls}); err != nil {
			return nil, err
		}
	}
	return calls, nil
}

type bootstrapVerifier struct {
	trace string
	index agentcontrol.Index
	calls map[string]*callpbv1.Call
}

func (v *bootstrapVerifier) Export(_ context.Context, records []sdklog.Record) error {
	for _, r := range records {
		if r.TraceID().String() != v.trace {
			return errors.New("bootstrap contains a foreign trace")
		}
		if agentcontrol.IsRecord(r) {
			a, s, err := agentcontrol.Decode(r)
			if err != nil {
				return err
			}
			ns := agentcontrol.Namespace{}
			if a != nil {
				ns = a.Namespace
			} else {
				ns = s.Namespace
			}
			if ns.Trace != v.trace {
				return errors.New("control namespace trace mismatch")
			}
			if _, err := v.index.ApplyRecord(r); err != nil {
				return err
			}
			continue
		}
		payload := false
		r.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.ContentTypeAttr && kv.Value.AsString() == telemetryattrs.CallPayloadContentType {
				payload = true
			}
			return true
		})
		if !payload {
			return errors.New("bootstrap contains non-control, non-payload log")
		}
		if r.Body().Kind() != log.KindBytes {
			return errors.New("call payload body is not bytes")
		}
		var c callpbv1.Call
		if err := proto.Unmarshal(r.Body().AsBytes(), &c); err != nil {
			return err
		}
		if old := v.calls[c.Digest]; old != nil && !proto.Equal(old, &c) {
			return fmt.Errorf("conflicting payload %s", c.Digest)
		}
		v.calls[c.Digest] = &c
	}
	return nil
}
func (*bootstrapVerifier) ForceFlush(context.Context) error { return nil }
func (*bootstrapVerifier) Shutdown(context.Context) error   { return nil }
func ValidateBootstrap(ctx context.Context, header BootstrapHeader, batches []BootstrapBatch) error {
	want, err := header.Completion.Expectation()
	if err != nil {
		return err
	}
	v := &bootstrapVerifier{trace: header.TraceID, calls: map[string]*callpbv1.Call{}}
	for _, b := range batches {
		if b.Logs != nil {
			if err := telemetry.ReexportLogsFromPB(ctx, v, b.Logs); err != nil {
				return err
			}
		}
	}
	if err := v.index.Verify(want); err != nil {
		return err
	}
	var roots []string
	for _, a := range v.index.Agents() {
		roots = append(roots, a.Digest)
	}
	_, err = VerifyClosure(roots, func(d string) (*callpbv1.Call, error) {
		c := v.calls[d]
		if c == nil {
			return nil, fmt.Errorf("missing bootstrap call %s", d)
		}
		return c, nil
	})
	return err
}
