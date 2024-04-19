package build

import (
	"strings"

	"github.com/moby/buildkit/session/sshforward/sshprovider"
)

// ParseSSH parses --ssh
func ParseSSH(inp []string) ([]sshprovider.AgentConfig, error) {
	configs := make([]sshprovider.AgentConfig, 0, len(inp))
	for _, v := range inp {
		parts := strings.SplitN(v, "=", 2)
		cfg := sshprovider.AgentConfig{
			ID: parts[0],
		}
		if len(parts) > 1 {
			cfg.Paths = strings.Split(parts[1], ",")
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}
