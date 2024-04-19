package integration

import (
	"net"

	"github.com/Microsoft/go-winio"

	// include npipe connhelper for windows tests
	_ "github.com/moby/buildkit/client/connhelper/npipe"
)

var socketScheme = "npipe://"

// abstracted function to handle pipe dialing on windows.
// some simplification has been made to discard timeout param.
func dialPipe(address string) (net.Conn, error) {
	return winio.DialPipe(address, nil)
}
