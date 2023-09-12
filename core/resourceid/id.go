package resourceid

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/idproto"
	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

// Digestible is any object which can return a digest of its content.
//
// It is used to record the request's result as an output of the request's
// vertex in the progress stream.
type Digestible interface {
	Digest() (digest.Digest, error)
}

func New[T any](typeName string) ID[T] {
	return ID[T]{idproto.New(typeName)}
}

func FromProto[T any](proto *idproto.ID) ID[T] {
	return ID[T]{proto}
}

// ID is a thin wrapper around *idproto.ID that is primed to expect a
// particular return type.
type ID[T any] struct {
	*idproto.ID
}

func (id ID[T]) ResourceTypeName() string {
	var t T
	name := fmt.Sprintf("%T", t)
	return strings.TrimPrefix(name, "*")
}

// TODO these type hints aren't doing us any favors here, since we don't
// actually check the embedded type.
func Decode(id string) (*idproto.ID, error) {
	if id == "" {
		// TODO(vito): this is a little awkward, can we avoid
		// it? adding initially for backwards compat, since some
		// places compare with empty string
		return nil, nil
	}
	bytes, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return nil, err
	}
	var idproto idproto.ID
	if err := proto.Unmarshal(bytes, &idproto); err != nil {
		return nil, err
	}
	return &idproto, nil
}

func (id ID[T]) String() string {
	proto, err := proto.Marshal(id.ID)
	if err != nil {
		panic(err)
	}
	return base64.URLEncoding.EncodeToString(proto)
}

// Decode base64-decodes and JSON unmarshals an ID into the object T
func (id ID[T]) Decode() (*T, error) {
	return nil, errors.New("TODO replace ID.Decode with resolving the ID and asserting on the return type")
}
