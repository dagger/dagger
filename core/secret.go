package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// Secret is a content-addressed secret.
type Secret struct {
	ID     SecretID `json:"id"`
	Digest string   `json:"digest"`
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

func (id SecretID) String() string { return string(id) }

func (id SecretID) Digest() (string, error) {
	b, err := base64.StdEncoding.DecodeString(string(id))
	if err != nil {
		return "", err
	}

	_, digest, ok := strings.Cut(string(b), "\x00")
	if !ok {
		// FIXME create a dedicated error
		return "", errors.New("malformed secret ID")
	}

	return digest, nil
}

func NewSecretID(name, plaintext string) SecretID {
	digestBytes := sha256.Sum256([]byte(plaintext))

	idBytes := []byte(name + "\x00" + string(digestBytes[:]))

	id := base64.StdEncoding.EncodeToString(idBytes)
	return SecretID(id)
}

// secretIDPayload is the inner content of a SecretID.
type secretIDPayload struct {
	FromFile    FileID `json:"file,omitempty"`
	FromHostEnv string `json:"host_env,omitempty"`

	Name   string
	Digest string
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
