package auth

import (
	"slices"

	"google.golang.org/grpc"
)

// RegisterCredentialsServer registers only the Credentials method of the
// Auth service. The token methods are not registered, so the client does not
// advertise them and the engine cannot call them.
func RegisterCredentialsServer(s *grpc.Server, srv AuthServer) {
	desc := _Auth_serviceDesc
	desc.Methods = slices.DeleteFunc(slices.Clone(desc.Methods), func(m grpc.MethodDesc) bool {
		return m.MethodName != "Credentials"
	})
	desc.Streams = nil
	s.RegisterService(&desc, srv)
}
