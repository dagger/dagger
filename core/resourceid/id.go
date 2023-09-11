package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
)

// Digestible is any object which can return a digest of its content.
//
// It is used to record the request's result as an output of the request's
// vertex in the progress stream.
type Digestible interface {
	Digest() (digest.Digest, error)
}

type ID[T any] string

func (id ID[T]) ResourceTypeName() string {
	var t T
	name := fmt.Sprintf("%T", t)
	return strings.TrimPrefix(name, "*")
}

func (id ID[T]) String() string {
	return string(id)
}

func (id ID[T]) Digest() (digest.Digest, error) {
	obj, err := id.Decode()
	if err != nil {
		return "", err
	}
	if obj == nil {
		return digest.FromString(id.String()), nil
	}
	digestible, ok := any(obj).(Digestible)
	if !ok {
		return digest.FromString(id.String()), nil
	}
	return digestible.Digest()
}

// Decode base64-decodes and JSON unmarshals an ID into the object T
func (id ID[T]) Decode() (*T, error) {
	var payload T
	if id == "" {
		return &payload, nil
	}

	actualType, idEnc, ok := strings.Cut(string(id), ":")
	if !ok {
		return nil, fmt.Errorf("malformed ID: %v", id)
	}

	if actualType != id.ResourceTypeName() {
		return nil, fmt.Errorf("ID type mismatch: %v != %v", actualType, id.ResourceTypeName())
	}

	jsonBytes, err := base64.StdEncoding.DecodeString(idEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %T bytes: %v: %w", payload, id, err)
	}

	if err := json.Unmarshal(jsonBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %T: %w", payload, err)
	}
	return &payload, nil
}

// Encode JSON marshals and base64-encodes an arbitrary payload.
func Encode[T any, I ID[T]](payload *T) (I, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to json marshal %T: %w", payload, err)
	}

	idEnc := base64.StdEncoding.EncodeToString(jsonBytes)

	var t T
	typeName := strings.TrimPrefix(fmt.Sprintf("%T", t), "*")
	id := I(fmt.Sprintf("%s:%s", typeName, idEnc))
	return id, nil
}

func TypeName(id string) (string, error) {
	actualType, _, ok := strings.Cut(string(id), ":")
	if !ok {
		return "", fmt.Errorf("malformed ID: %v", id)
	}
	return actualType, nil
}
