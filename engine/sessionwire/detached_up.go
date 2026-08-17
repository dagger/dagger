package sessionwire

// NetworkProtocol is the serialized protocol used by detached-up recipes.
// Keeping these wire-only shapes outside core lets cross-platform clients
// decode saved results without importing the engine implementation.
type NetworkProtocol string

const (
	NetworkProtocolTCP NetworkProtocol = "TCP"
	NetworkProtocolUDP NetworkProtocol = "UDP"
)

type PortForward struct {
	Frontend *int            `json:"frontend"`
	Backend  int             `json:"backend"`
	Protocol NetworkProtocol `json:"protocol"`
}

type DetachedUpPort struct {
	Port     int             `json:"port"`
	Protocol NetworkProtocol `json:"protocol"`
}

type DetachedUpService struct {
	Name         string           `json:"name"`
	ServiceID    string           `json:"serviceId"`
	Native       bool             `json:"native"`
	PortMappings []PortForward    `json:"portMappings"`
	BackendPorts []DetachedUpPort `json:"backendPorts"`
}

type DetachedUpResult struct {
	Services []DetachedUpService `json:"services"`
}
