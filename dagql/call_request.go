package dagql

// CallRequest is the mutable planning-time wrapper around the semantic
// ResultCallFrame shape, plus request-only cache policy that does not belong in
// persisted provenance.
type CallRequest struct {
	*ResultCallFrame

	ConcurrencyKey string
	TTL            int64
	DoNotCache     bool
	IsPersistable  bool
}

func (req *CallRequest) Clone() *CallRequest {
	if req == nil {
		return nil
	}
	frame := req.ResultCallFrame.clone()
	if frame == nil {
		frame = &ResultCallFrame{}
	}
	return &CallRequest{
		ResultCallFrame: frame,
		ConcurrencyKey:  req.ConcurrencyKey,
		TTL:             req.TTL,
		DoNotCache:      req.DoNotCache,
		IsPersistable:   req.IsPersistable,
	}
}

func (req *CallRequest) ToResultCallFrame() (*ResultCallFrame, error) {
	if req == nil {
		return nil, nil
	}
	return req.ResultCallFrame.clone(), nil
}
