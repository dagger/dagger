package integration

import (
	"net"

	"github.com/Microsoft/go-winio"

	// include npipe connhelper for windows tests
	_ "github.com/dagger/dagger/buildkit/client/connhelper/npipe"
)

var socketScheme = "npipe://"

var windowsImagesMirrorMap = map[string]string{
	// TODO(profnandaa): currently, amd64 only, to revisit for other archs.
	"nanoserver:latest": "mcr.microsoft.com/windows/nanoserver:ltsc2022",
	"servercore:latest": "mcr.microsoft.com/windows/servercore:ltsc2022",
	"busybox:latest":    "registry.k8s.io/e2e-test-images/busybox@sha256:6d854ffad9666d2041b879a1c128c9922d77faced7745ad676639b07111ab650",
	// nanoserver with extra binaries, like fc.exe
	// TODO(profnandaa): get an approved/compliant repo, placeholder for now
	// see dockerfile here - https://github.com/microsoft/windows-container-tools/pull/178
	"nanoserver:plus":         "docker.io/wintools/nanoserver:ltsc2022",
	"nanoserver:plus-busybox": "docker.io/wintools/nanoserver:ltsc2022",
}

// abstracted function to handle pipe dialing on windows.
// some simplification has been made to discard timeout param.
func dialPipe(address string) (net.Conn, error) {
	return winio.DialPipe(address, nil)
}
