package dagql

import "context"

// CallObserver prepares facts from resolved inputs before cache lookup,
// independently of telemetry.
type CallObserver func(context.Context, *CallRequest) context.Context

func (s *Server) ObserveCalls(observer CallObserver) {
	s.callObserver = observer
}
