package secretprovider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/approle"
)

type dataWithTTL struct {
	expiresAt time.Time
	data      map[string]any
}

var (
	mutex       sync.Mutex
	vaultClient *vault.Client
	vaultCache  = make(map[string]dataWithTTL)
)

// HashiCorp Vault provider for SecretProvider
func vaultProvider(ctx context.Context, pathWithQuery string) ([]byte, error) {
	mutex.Lock()
	defer mutex.Unlock()

	parsed, err := url.Parse(pathWithQuery)
	if err != nil {
		return nil, err
	}

	// this is just path part without the query params such as ttl
	key := parsed.Path

	var ttl time.Duration
	ttlStr := strings.TrimSpace(parsed.Query().Get("ttl"))
	if ttlStr != "" {
		ttl, err = time.ParseDuration(ttlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl %q provided for secret %q: %w", ttlStr, key, err)
		}
	}

	// KVv2 mount path. Default "secret"
	mount := os.Getenv("VAULT_PATH_PREFIX")
	if mount == "" {
		mount = "secret"
	}

	// split key into path and field, e.g. "path/to/secret.field"
	keyParts := strings.Split(key, ".")
	if len(keyParts) != 2 {
		return nil, fmt.Errorf("invalid key format: %s", key)
	}
	secretPath := keyParts[0]
	secretField := keyParts[1]

	if existing, ok := vaultCache[key]; !ok || hasExpired(existing) {
		// check if client is initialized
		if vaultClient == nil {
			err := vaultConfigureClient(ctx)
			if err != nil {
				return nil, err
			}
		}

		// read the secret
		s, err := vaultClient.KVv2(mount).Get(ctx, secretPath)
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", secretPath, err)
		}
		data := dataWithTTL{
			data: s.Data,
		}

		if ttl > 0 {
			data.expiresAt = time.Now().Add(ttl)
		}

		// cache response
		vaultCache[key] = data
	}

	return []byte(vaultCache[key].data[secretField].(string)), nil
}

func hasExpired(data dataWithTTL) bool {
	// if no ttl set, assume no ttl required
	if data.expiresAt.IsZero() {
		return false
	}

	if data.expiresAt.After(time.Now()) {
		return false
	}

	return true
}

// Load configuration from environment and create a new vault client
func vaultConfigureClient(ctx context.Context) error {
	config := vault.DefaultConfig()

	// Load configuration from environment
	err := config.ReadEnvironment()
	if err != nil {
		return err
	}

	// Create client. Auths with VAULT_TOKEN by default
	client, err := vault.NewClient(config)
	if err != nil {
		return err
	}

	// Use AppRole if provided
	roleID := os.Getenv("VAULT_APPROLE_ROLE_ID")
	if roleID != "" {
		secretID := &auth.SecretID{FromEnv: "VAULT_APPROLE_SECRET_ID"}
		// Authenticate
		appRoleAuth, err := auth.NewAppRoleAuth(
			roleID,
			secretID,
		)
		if err != nil {
			return fmt.Errorf("unable to initialize Vault AppRole auth method: %w", err)
		}

		authInfo, err := client.Auth().Login(ctx, appRoleAuth)
		if err != nil {
			return fmt.Errorf("unable to login to Vault AppRole auth method: %w", err)
		}
		if authInfo == nil {
			return fmt.Errorf("no auth info was returned after Vault AppRole login")
		}
	}

	// Set client
	vaultClient = client
	return nil
}
