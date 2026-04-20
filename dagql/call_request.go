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
		ResultCall:     frame,
		ConcurrencyKey: req.ConcurrencyKey,
		TTL:            req.TTL,
		DoNotCache:     req.DoNotCache,
		IsPersistable:  req.IsPersistable,
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
