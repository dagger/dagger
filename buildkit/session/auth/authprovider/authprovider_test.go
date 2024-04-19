package authprovider

import (
	"context"
	"testing"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchTokenCaching(t *testing.T) {
	cfg := &configfile.ConfigFile{
		AuthConfigs: map[string]types.AuthConfig{
			dockerHubConfigfileKey: {Username: "user", RegistryToken: "hunter2"},
		},
	}
	p := NewDockerAuthProvider(cfg, nil).(*authProvider)
	res, err := p.FetchToken(context.Background(), &auth.FetchTokenRequest{Host: dockerHubRegistryHost})
	require.NoError(t, err)
	assert.Equal(t, "hunter2", res.Token)

	cfg.AuthConfigs[dockerHubConfigfileKey] = types.AuthConfig{Username: "user", RegistryToken: "hunter3"}
	res, err = p.FetchToken(context.Background(), &auth.FetchTokenRequest{Host: dockerHubRegistryHost})
	require.NoError(t, err)

	// Verify that we cached the result instead of returning hunter3.
	assert.Equal(t, "hunter2", res.Token)
}
