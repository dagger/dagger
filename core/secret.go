package core

import (
	"context"
	"fmt"
	"os"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// Secret is a content-addressed secret.
type Secret struct {
	// Name specifies the arbitrary name/id of the secret.
	Name string `json:"name,omitempty"`

	// FromFile specifies the FileID it is based off.
	//
	// Deprecated: this shouldn't be used as it can leak secrets in the cache.
	// Use the setSecret API instead.
	FromFile FileID `json:"file,omitempty"`

	// FromHostEnv specifies the FileID it is based off.
	//
	// Deprecated: use the setSecret API instead.
	FromHostEnv string `json:"host_env,omitempty"`
}

func NewSecretFromFile(fileID FileID) *Secret {
	return &Secret{FromFile: fileID}
}

func NewSecretFromHostEnv(name string) *Secret {
	return &Secret{FromHostEnv: name}
}

// SecretID is an opaque value representing a content-addressed secret.
type SecretID string

func NewDynamicSecret(name string) *Secret {
	return &Secret{
		Name: name,
	}
}

func (id SecretID) ToSecret() (*Secret, error) {
	var secret Secret
	if err := decodeID(&secret, id); err != nil {
		return nil, err
	}

	return &secret, nil
}

func (id SecretID) String() string { return string(id) }

func (secret *Secret) Clone() *Secret {
	cp := *secret
	return &cp
}

func (secret *Secret) ID() (SecretID, error) {
	return encodeID[SecretID](secret)
}

func (secret *Secret) IsOldFormat() bool {
	return secret.FromFile != "" || secret.FromHostEnv != ""
}

func (secret *Secret) LegacyPlaintext(ctx context.Context, gw bkgw.Client) ([]byte, error) {
	if secret.FromFile != "" {
		file, err := secret.FromFile.ToFile()
		if err != nil {
			return nil, err
		}
		return file.Contents(ctx, gw)
	}

	if secret.FromHostEnv != "" {
		return []byte(os.Getenv(secret.FromHostEnv)), nil
	}

	return nil, fmt.Errorf("plaintext: empty secret?")
}
