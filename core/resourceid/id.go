package resourceid

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/idproto"
	"github.com/opencontainers/go-digest"
	"google.golang.org/protobuf/proto"
)

func New[T any](typeName string) *ID[T] {
	return &ID[T]{idproto.New(typeName)}
}

func FromProto[T any](proto *idproto.ID) *ID[T] {
	return &ID[T]{proto}
}

// ID is a thin wrapper around *idproto.ID that is primed to expect a
// particular return type.
type ID[T any] struct {
	*idproto.ID
}

func (id *ID[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(id.String())
}

func (id *ID[T]) UnmarshalJSON(p []byte) error {
	var str string
	if err := json.Unmarshal(p, &str); err != nil {
		return err
	}

	idp, err := Decode(str)
	if err != nil {
		return err
	}

	id.ID = idp

	return nil
}

func (id *ID[T]) ResourceTypeName() string {
	var t T
	name := fmt.Sprintf("%T", t)
	return strings.TrimPrefix(name, "*")
}

func DecodeID[T any](id string) (*ID[T], error) {
	if id == "" {
		// TODO(vito): this is a little awkward, can we avoid
		// it? adding initially for backwards compat, since some
		// places compare with empty string
		return nil, nil
	}
	idp, err := Decode(id)
	if err != nil {
		return nil, err
	}
	return FromProto[T](idp), nil
}

func DecodeFromID[T any](id string, cache IDCache) (T, error) {
	var zero T
	if id == "" {
		// TODO(vito): this is a little awkward, can we avoid
		// it? adding initially for backwards compat, since some
		// places compare with empty string
		return zero, nil
	}
	idp, err := Decode(id)
	if err != nil {
		return zero, err
	}
	return FromProto[T](idp).Resolve(cache)
}

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

func (id *ID[T]) String() string {
	proto, err := proto.Marshal(id.ID)
	if err != nil {
		panic(err)
	}
	return base64.URLEncoding.EncodeToString(proto)
}

type IDCache interface {
	Get(digest.Digest) (any, error)
}

// Resolve
func (id *ID[T]) Resolve(cache IDCache) (T, error) {
	var zero T

	dig, err := id.Digest()
	if err != nil {
		return zero, err
	}
	obj, err := cache.Get(dig)
	if err != nil {
		return zero, err
	}
	return obj.(T), nil
}
