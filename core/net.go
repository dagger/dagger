package core

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// Port configures a port to exposed from a container or service.
type Port struct {
	Port                        int             `field:"true" doc:"The port number."`
	Protocol                    NetworkProtocol `field:"true" doc:"The transport layer protocol."`
	Description                 *string         `field:"true" doc:"The port description."`
	ExperimentalSkipHealthcheck bool            `field:"true" doc:"Skip the health check when run as a service."`
}

// NewPortFromOCI parses an OCI port spec (e.g. 8080/tcp) into a Port struct
func NewPortFromOCI(s string) (p Port, _ error) {
	port, protoStr, ok := strings.Cut(s, "/")
	if !ok {
		return p, fmt.Errorf("unable to parse OCI port: missing / delimiter")
	}
	portNr, err := strconv.Atoi(port)
	if err != nil {
		return p, fmt.Errorf("unable to parse OCI port: unable to parse integer %s", port)
	}
	proto, err := NetworkProtocols.Lookup(strings.ToUpper(protoStr))
	if err != nil {
		return p, fmt.Errorf("unable to parse OCI port: unable to lookup %s: %w", protoStr, err)
	}
	p = Port{
		Port:     portNr,
		Protocol: proto,
	}
	return p, nil
}

func (Port) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Port",
		NonNull:   true,
	}
}

func (Port) TypeDescription() string {
	return "A port exposed by a container."
}

// NetworkProtocol is a GraphQL enum type.
type NetworkProtocol string

var NetworkProtocols = dagql.NewEnum[NetworkProtocol]()

var (
	NetworkProtocolTCP = NetworkProtocols.Register("TCP")
	NetworkProtocolUDP = NetworkProtocols.Register("UDP")
)

func (proto NetworkProtocol) Type() *ast.Type {
	return &ast.Type{
		NamedType: "NetworkProtocol",
		NonNull:   true,
	}
}

func (proto NetworkProtocol) TypeDescription() string {
	return "Transport layer network protocol associated to a port."
}

func (proto NetworkProtocol) Decoder() dagql.InputDecoder {
	return NetworkProtocols
}

func (proto NetworkProtocol) ToLiteral() call.Literal {
	return NetworkProtocols.Literal(proto)
}

// Network returns the value appropriate for the "network" argument to Go
// net.Dial, and for appending to the port number to form the key for the
// ExposedPorts object in the OCI image config.
func (proto NetworkProtocol) Network() string {
	return strings.ToLower(string(proto))
}

type PortForward struct {
	Frontend *int            `doc:"Port to expose to clients. If unspecified, a default will be chosen." json:"frontend,omitempty"`
	Backend  int             `doc:"Destination port for traffic." json:"backend"`
	Protocol NetworkProtocol `doc:"Transport layer protocol to use for traffic." default:"TCP" json:"protocol,omitempty"`
}

func (pf PortForward) TypeName() string {
	return "PortForward"
}

func (pf PortForward) TypeDescription() string {
	return "Port forwarding rules for tunneling network traffic."
}

func (pf PortForward) FrontendOrBackendPort() int {
	if pf.Frontend != nil {
		return *pf.Frontend
	}
	return pf.Backend
}

// ParsePortMapping parses a Docker-like "hostPort:containerPort" string into a PortForward.
func ParsePortMapping(s string) (PortForward, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return PortForward{}, fmt.Errorf("invalid port mapping %q: expected \"hostPort:containerPort\"", s)
	}
	frontend, err := strconv.Atoi(parts[0])
	if err != nil {
		return PortForward{}, fmt.Errorf("invalid host port in %q: %w", s, err)
	}
	if frontend < 1 || frontend > 65535 {
		return PortForward{}, fmt.Errorf("host port %d in %q out of range (1-65535)", frontend, s)
	}
	backend, err := strconv.Atoi(parts[1])
	if err != nil {
		return PortForward{}, fmt.Errorf("invalid container port in %q: %w", s, err)
	}
	if backend < 1 || backend > 65535 {
		return PortForward{}, fmt.Errorf("container port %d in %q out of range (1-65535)", backend, s)
	}
	return PortForward{
		Frontend: &frontend,
		Backend:  backend,
		Protocol: NetworkProtocolTCP,
	}, nil
}
