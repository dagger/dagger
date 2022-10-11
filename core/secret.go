package core

import (
	"context"
	"fmt"
	"os"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// Secret is a content-addressed secret.
type Secret struct {
	ID SecretID `json:"id"`
}

func NewSecret(id SecretID) *Secret {
	return &Secret{ID: id}
}

func NewSecretFromFile(fileID FileID) (*Secret, error) {
	id, err := (&secretIDPayload{FromFile: fileID}).Encode()
	if err != nil {
		return nil, err
	}

	return NewSecret(id), nil
}

func NewSecretFromHostEnv(name string) (*Secret, error) {
	id, err := (&secretIDPayload{FromHostEnv: name}).Encode()
	if err != nil {
		return nil, err
	}

	return NewSecret(id), nil
}

// SecretID is an opaque value representing a content-addressed secret.
type SecretID string

// secretIDPayload is the inner content of a SecretID.
type secretIDPayload struct {
	FromFile    FileID `json:"file,omitempty"`
	FromHostEnv string `json:"host_env,omitempty"`
}

// Encode returns the opaque string ID representation of the secret.
func (payload *secretIDPayload) Encode() (SecretID, error) {
	id, err := encodeID(payload)
	if err != nil {
		return "", err
	}

	return SecretID(id), nil
}

func (id SecretID) decode() (*secretIDPayload, error) {
	var payload secretIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

func (secret *Secret) Plaintext(ctx context.Context, gw bkgw.Client) ([]byte, error) {
	payload, err := secret.ID.decode()
	if err != nil {
		return nil, err
	}

	if payload.FromFile != "" {
		file := &File{ID: payload.FromFile}
		return file.Contents(ctx, gw)
	}

	if payload.FromHostEnv != "" {
		return []byte(os.Getenv(payload.FromHostEnv)), nil
	}

	return nil, fmt.Errorf("plaintext: empty secret?")
}
