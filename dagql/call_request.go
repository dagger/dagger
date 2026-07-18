package dagql

import "context"

// CallRequest is the mutable planning-time wrapper around the semantic
// ResultCall shape, plus request-only cache policy that does not belong in
// persisted provenance.
type CallRequest struct {
	*ResultCall

	ConcurrencyKey string
	TTL            int64
	DoNotCache     bool
	IsPersistable  bool

	// PassthroughTelemetry keeps the call span available for trace metadata while
	// asking the UI to show its children in its place.
	PassthroughTelemetry bool

	// ReceiverTypeName is the immediate receiver's GraphQL type name, stamped
	// lookup-free at the object call site (objects.go) from r.class.inner.Type().Name().
	// It is request-only carrier state (never digested, never persisted) consumed by
	// core.AroundFunc to compute the static profile-skip decision and stamp it onto
	// the call frame (ResultCall.ProfileSkip) — so the predicate needs no egraphMu
	// receiver-resolution lookup per call.
	ReceiverTypeName string

	// CacheEvidence is the per-invocation cache-decision evidence carrier
	// (request-only carrier state like ReceiverTypeName: never digested, never
	// persisted, excluded from Clone). core.AroundFunc allocates it exactly
	// when this call's span records and the call is not ProfileSkip-classified;
	// getOrInitCallInner fills it along the existing decision flow; AroundFunc's
	// completion callback stamps it onto the caller's span. Nil means "record
	// nothing". It is scoped to one invocation — internal CallRequest
	// constructions that never pass through AroundFunc leave it nil.
	CacheEvidence *CacheDecision
}

func (req *CallRequest) Clone() *CallRequest {
	if req == nil {
		return nil
	}
	frame := req.ResultCall.clone()
	if frame == nil {
		frame = &ResultCall{}
	}
	return &CallRequest{
		ResultCall:           frame,
		ConcurrencyKey:       req.ConcurrencyKey,
		TTL:                  req.TTL,
		DoNotCache:           req.DoNotCache,
		IsPersistable:        req.IsPersistable,
		PassthroughTelemetry: req.PassthroughTelemetry,
		ReceiverTypeName:     req.ReceiverTypeName,
		// CacheEvidence is deliberately NOT carried over: it is per-invocation
		// state owned by the one AroundFunc-wrapped invocation that allocated
		// it; a cloned request is a different (or internal) invocation and
		// sharing the pointer would let two invocations write into one record.
	}
}

func (req *CallRequest) ToResultCall() (*ResultCall, error) {
	if req == nil {
		return nil, nil
	}
	return req.ResultCall.clone(), nil
}

func (req *CallRequest) Arg(name string) *ResultCallArg {
	if req == nil {
		return nil
	}
	for _, arg := range req.Args {
		if arg != nil && arg.Name == name {
			return arg
		}
	}
	return nil
}

func (req *CallRequest) HasArg(name string) bool {
	return req.Arg(name) != nil
}

func (req *CallRequest) SetArg(arg *ResultCallArg) {
	if req == nil || arg == nil {
		return
	}
	for i, existing := range req.Args {
		if existing != nil && existing.Name == arg.Name {
			req.Args[i] = arg
			return
		}
	}
	req.Args = append(req.Args, arg)
}

func (req *CallRequest) DeleteArg(name string) {
	if req == nil || len(req.Args) == 0 {
		return
	}
	for i, arg := range req.Args {
		if arg == nil || arg.Name != name {
			continue
		}
		req.Args = append(req.Args[:i], req.Args[i+1:]...)
		return
	}
}

func (req *CallRequest) SetArgInput(ctx context.Context, name string, input Input, sensitive bool) error {
	if req == nil {
		return nil
	}
	arg, err := resultCallArgFromInput(ctx, name, input, sensitive)
	if err != nil {
		return err
	}
	req.SetArg(arg)
	return nil
}
