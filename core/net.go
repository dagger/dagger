package core

import "strings"

// Port configures a port to exposed from a container or service.
type Port struct {
	Port        int             `json:"port"`
	Protocol    NetworkProtocol `json:"protocol"`
	Description *string         `json:"description,omitempty"`
}

// NetworkProtocol is a string deriving from NetworkProtocol enum
type NetworkProtocol string

const (
	NetworkProtocolTCP NetworkProtocol = "TCP"
	NetworkProtocolUDP NetworkProtocol = "UDP"
)

func (proto NetworkProtocol) EnumName() string {
	return string(proto)
}

// Network returns the value appropriate for the "network" argument to Go
// net.Dial, and for appending to the port number to form the key for the
// ExposedPorts object in the OCI image config.
func (proto NetworkProtocol) Network() string {
	return strings.ToLower(string(proto))
}

type PortForward struct {
	Frontend int             `json:"frontend"`
	Backend  int             `json:"backend"`
	Protocol NetworkProtocol `json:"protocol"`
}

func (pf PortForward) FrontendOrBackendPort() int {
	if pf.Frontend != 0 {
		return pf.Frontend
	}
	return pf.Backend
}
