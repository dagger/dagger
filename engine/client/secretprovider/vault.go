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

func splitVaultPath(path string) (mount, secretPath, secretField string, err error) {
	mount, rest, found := strings.Cut(path, "/")
	if !found {
		return "", "", "", fmt.Errorf("missing \"/\" separator")
	}
	if mount == "" {
		return "", "", "", fmt.Errorf("missing mount")
	}
	n := strings.LastIndex(rest, ".")
	if n < 0 {
		return "", "", "", fmt.Errorf("missing field name after \".\"")
	}
	return mount, rest[:n], rest[n+1:], nil
}

// HashiCorp Vault provider for SecretProvider
func vaultProvider(ctx context.Context, pathWithQuery string) ([]byte, error) {
	mutex.Lock()
	defer mutex.Unlock()

	parsed, err := url.Parse(pathWithQuery)
	if err != nil {
		return nil, err
	}

	// Vault path, excluding query params such as ttl
	path := parsed.Path

	// Deprecated: Legacy VAULT_PATH_PREFIX variable. It will be removed in a future release. Use the full path in the secret path instead.
	pathPrefix := os.Getenv("VAULT_PATH_PREFIX")
	if pathPrefix != "" {
		path = strings.TrimRight(pathPrefix, "/") + "/" + path
	}

	// Get the mount, secret path and secret field from the path
	mount, secretPath, secretField, err := splitVaultPath(path)
	if err != nil {
		return nil, fmt.Errorf("invalid path %q: %w", path, err)
	}

	// Get the TTL from the query parameters, if provided
	var ttl time.Duration
	ttlStr := strings.TrimSpace(parsed.Query().Get("ttl"))
	if ttlStr != "" {
		ttl, err = time.ParseDuration(ttlStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl %q provided for secret %q: %w", ttlStr, path, err)
		}
	}

	// Cache for the whole secret path, not just the field, since Vault returns all fields in a secret at once
	cacheKey := mount + "/" + secretPath

	// If the secret is not in the cache or has expired, fetch it from Vault
	if existing, ok := vaultCache[cacheKey]; !ok || hasExpired(existing) {
		// check if client is initialized
		if vaultClient == nil {
			err := vaultConfigureClient(ctx)
			if err != nil {
				return nil, err
			}
		}

		// Read the secret
		s, err := vaultClient.KVv2(mount).Get(ctx, secretPath)
		if err != nil {
			return nil, fmt.Errorf("mount path %q: %w", secretPath, err)
		}
		data := dataWithTTL{
			data: s.Data,
		}

		if ttl > 0 {
			data.expiresAt = time.Now().Add(ttl)
		}

		vaultCache[cacheKey] = data
	}

	secretDataAny := vaultCache[cacheKey].data[secretField]
	if secretDataAny == nil {
		return nil, fmt.Errorf("secret %q not found in path \"%s/%s\"", secretField, mount, secretPath)
	}
	secretData, ok := secretDataAny.(string)
	if !ok {
		return nil, fmt.Errorf("secret %q in path \"%s/%s\" is not a string", secretField, mount, secretPath)
	}
	return []byte(secretData), nil
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
		var opts []auth.LoginOption

		// Sets auth mount path. Default is "approle"
		authMethod := os.Getenv("VAULT_APPROLE_MOUNT_PATH")
		if authMethod != "" {
			opts = append(opts, auth.WithMountPath(authMethod))
		}

		// Get SecretID
		secretID := &auth.SecretID{FromEnv: "VAULT_APPROLE_SECRET_ID"}

		// Authenticate
		appRoleAuth, err := auth.NewAppRoleAuth(
			roleID,
			secretID,
			opts...,
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
	} else if client.Token() == "" {
		if os.Getenv("VAULT_ADDR") == "" {
			return fmt.Errorf("VAULT_ADDR must be set when using Vault OIDC fallback auth")
		}

		cachedToken, err := loadCachedVaultToken()
		if err != nil {
			return fmt.Errorf("failed loading cached Vault token: %w", err)
		}
		if cachedToken != "" {
			client.SetToken(cachedToken)

			if err := validateVaultToken(ctx, client); err != nil {
				if !isVaultInvalidTokenError(err) {
					return fmt.Errorf("failed validating cached Vault token: %w", err)
				}

				fmt.Fprintln(os.Stderr, "Cached Vault token is invalid; starting OIDC login...")
				if err := clearCachedVaultToken(); err != nil {
					return fmt.Errorf("failed clearing cached Vault token: %w", err)
				}
				client.SetToken("")
			}
		}

		if client.Token() == "" {
			ttl, err := vaultOIDCLogin(ctx, client)
			if err != nil {
				return fmt.Errorf("vault OIDC login failed: %w", err)
			}

			if err := saveCachedVaultToken(client.Token(), ttl); err != nil {
				return fmt.Errorf("failed saving cached Vault token: %w", err)
			}
		}
	}

	// Set client
	vaultClient = client
	return nil
}
