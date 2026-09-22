package agentcontrol

import (
	"fmt"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	VersionAttr      = "dagger.io/agent.control.version"
	KindAttr         = "dagger.io/agent.control.kind"
	SessionAttr      = "dagger.io/agent.control.session"
	TraceAttr        = "dagger.io/agent.control.trace"
	IncarnationAttr  = "dagger.io/agent.control.incarnation"
	RevisionAttr     = "dagger.io/agent.control.revision"
	ParentAttr       = "dagger.io/agent.parent"
	CaptureErrorAttr = "dagger.io/agent.capture.error"
	PreTeardownAttr  = "dagger.io/agent.pre_teardown_state"
	FailureAttr      = "dagger.io/agent.failure"
	ActivityAttr     = "dagger.io/agent.activity"
	SubscriberAttr   = "dagger.io/agent.subscription.subscriber"
	StatesAttr       = "dagger.io/agent.subscription.states"
)

func record(ns Namespace, kind string, revision int64) log.Record {
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.Int(VersionAttr, Version), log.String(KindAttr, kind),
		log.String(SessionAttr, ns.Session), log.String(TraceAttr, ns.Trace),
		log.String(IncarnationAttr, ns.Incarnation), log.Int64(RevisionAttr, revision),
	)
	return rec
}

func (a Agent) Record() log.Record {
	rec := record(a.Namespace, "agent", a.Revision)
	rec.AddAttributes(
		log.String(telemetryattrs.AgentIDAttr, a.Handle),
		log.String(telemetryattrs.AgentNameAttr, a.Name),
		log.String(telemetryattrs.AgentCallDigestAttr, a.CallDigest),
		log.String(telemetryattrs.AgentSnapshotDigestAttr, a.Digest),
		log.String(telemetryattrs.AgentStateAttr, a.State),
		log.String(telemetryattrs.AgentWaitingOnAttr, a.WaitingOn),
		log.String(telemetryattrs.AgentStopReasonAttr, a.StopReason),
		log.String(ParentAttr, a.Parent), log.String(CaptureErrorAttr, a.CaptureError),
		log.String(PreTeardownAttr, a.PreTeardownState), log.String(FailureAttr, a.Failure),
		log.Int64(ActivityAttr, a.Activity.UnixNano()),
	)
	return rec
}

func (s Subscription) Record() log.Record {
	rec := record(s.Namespace, "subscription", s.Revision)
	states := make([]log.Value, len(s.States))
	for i, state := range s.States {
		states[i] = log.StringValue(state)
	}
	rec.AddAttributes(log.String(telemetryattrs.AgentIDAttr, s.Watched),
		log.String(SubscriberAttr, s.Subscriber), log.Slice(StatesAttr, states...))
	return rec
}

// IsRecord recognizes even unsupported/malformed control versions so they
// cannot leak into ordinary logs or be mistaken for the legacy split protocol.
func IsRecord(rec sdklog.Record) bool {
	found := false
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		if kv.Key == VersionAttr {
			found = true
			return false
		}
		return true
	})
	return found
}

// Decode rejects missing or wrongly typed fields instead of manufacturing
// completeness. Optional facts are explicit empty strings on the wire.
func Decode(rec sdklog.Record) (*Agent, *Subscription, error) {
	attrs := map[string]log.Value{}
	var decodeErr error
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		if _, ok := attrs[kv.Key]; ok {
			decodeErr = fmt.Errorf("duplicate control attribute %s", kv.Key)
		}
		attrs[kv.Key] = kv.Value
		return true
	})
	str := func(key string) string {
		v, ok := attrs[key]
		if !ok || v.Kind() != log.KindString {
			decodeErr = fmt.Errorf("missing or non-string control attribute %s", key)
			return ""
		}
		return v.AsString()
	}
	integer := func(key string) int64 {
		v, ok := attrs[key]
		if !ok || v.Kind() != log.KindInt64 {
			decodeErr = fmt.Errorf("missing or non-integer control attribute %s", key)
			return 0
		}
		return v.AsInt64()
	}
	if v := integer(VersionAttr); v != Version {
		return nil, nil, fmt.Errorf("unsupported agent control version %d", v)
	}
	ns := Namespace{Session: str(SessionAttr), Trace: str(TraceAttr), Incarnation: str(IncarnationAttr)}
	revision := integer(RevisionAttr)
	handle := str(telemetryattrs.AgentIDAttr)
	switch kind := str(KindAttr); kind {
	case "agent":
		a := Agent{Key: Key{ns, handle}, Revision: revision,
			Name: str(telemetryattrs.AgentNameAttr), CallDigest: str(telemetryattrs.AgentCallDigestAttr),
			Digest: str(telemetryattrs.AgentSnapshotDigestAttr), State: str(telemetryattrs.AgentStateAttr),
			WaitingOn: str(telemetryattrs.AgentWaitingOnAttr), StopReason: str(telemetryattrs.AgentStopReasonAttr),
			Parent: str(ParentAttr), CaptureError: str(CaptureErrorAttr), PreTeardownState: str(PreTeardownAttr),
			Failure: str(FailureAttr), Activity: time.Unix(0, integer(ActivityAttr)).UTC(),
		}
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		if err := a.Validate(); err != nil {
			return nil, nil, err
		}
		return &a, nil, nil
	case "subscription":
		s := Subscription{EdgeKey: EdgeKey{ns, handle, str(SubscriberAttr)}, Revision: revision}
		v, ok := attrs[StatesAttr]
		if !ok || v.Kind() != log.KindSlice {
			return nil, nil, fmt.Errorf("missing or non-array %s", StatesAttr)
		}
		for _, state := range v.AsSlice() {
			if state.Kind() != log.KindString {
				return nil, nil, fmt.Errorf("non-string subscription state")
			}
			s.States = append(s.States, state.AsString())
		}
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		if err := s.Validate(); err != nil {
			return nil, nil, err
		}
		return nil, &s, nil
	default:
		return nil, nil, fmt.Errorf("unknown agent control record kind %q", kind)
	}
}

func (idx *Index) ApplyRecord(rec sdklog.Record) (bool, error) {
	a, s, err := Decode(rec)
	if err != nil {
		return false, err
	}
	if a != nil {
		return idx.ApplyAgent(*a)
	}
	return idx.ApplySubscription(*s)
}
