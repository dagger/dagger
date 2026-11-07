package dagql

import "context"

// CallObserver observes resolved inputs, resolver completion before publication,
// and the final call result. The last callback also runs on cache hits and errors.
type CallObserver func(context.Context, *CallRequest) (context.Context, func(), func(AnyResult, error))

func (s *Server) ObserveCalls(observer CallObserver) {
	s.callObserver = observer
}
