package git

import (
	"slices"

	"google.golang.org/grpc"
)

// RegisterRemoteCredentialsServer registers only the methods that serve
// remote repository access: credentials and the host's Git configuration.
// The methods that read or change local checkouts are not registered, so the
// client does not advertise them and the engine cannot call them.
func RegisterRemoteCredentialsServer(s *grpc.Server, srv GitServer) {
	desc := _Git_serviceDesc
	desc.Methods = slices.DeleteFunc(slices.Clone(desc.Methods), func(m grpc.MethodDesc) bool {
		return m.MethodName != "GetCredential" && m.MethodName != "GetConfig"
	})
	desc.Streams = nil
	s.RegisterService(&desc, srv)
}
