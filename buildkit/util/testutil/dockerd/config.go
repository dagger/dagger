package dockerd

type Config struct {
	Features map[string]bool `json:"features,omitempty"`
	Mirrors  []string        `json:"registry-mirrors,omitempty"`
	Builder  BuilderConfig   `json:"builder,omitempty"`
}

type BuilderEntitlements struct {
	NetworkHost      bool `json:"network-host,omitempty"`
	SecurityInsecure bool `json:"security-insecure,omitempty"`
}

type BuilderConfig struct {
	Entitlements BuilderEntitlements `json:",omitempty"`
}
