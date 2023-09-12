package core

import (
	"fmt"

	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/core/socket"
)

// Object is a value that can be referred to by ID. Within a Dagger session the
// ID will always resolve to the same Object by means of a session-local cache.
//
// Objects are identified by their ID, which also doubles as a serializable
// constructor for the object. By default, an object's ID will be the query
// that led to it. A GraphQL resolver may choose to set an ID of its own, for
// example to return a reproducible object from a moving reference.
type Object[T Cloneable[T]] struct {
	ID    *idproto.ID
	Value *T
}

type Cloneable[T any] interface {
	Clone() *T
}

type ContainerID = resourceid.ID[Container]

type ServiceID = resourceid.ID[Service]

type CacheVolumeID = resourceid.ID[CacheVolume]

type DirectoryID = resourceid.ID[Directory]

type FileID = resourceid.ID[File]

type SecretID = resourceid.ID[Secret]

type ModuleID = resourceid.ID[Module]

type FunctionID = resourceid.ID[Function]

type TypeDefID = resourceid.ID[TypeDef]

type GeneratedCodeID = resourceid.ID[GeneratedCode]

// SocketID is in the socket package (to avoid circular imports)

// ResourceFromID returns the resource corresponding to the given ID.
func ResourceFromID(idStr string) (any, error) {
	id, err := resourceid.Decode(idStr)
	if err != nil {
		return nil, err
	}
	switch id.TypeName {
	case ContainerID{}.ResourceTypeName():
		return ContainerID{ID: id}.Decode()
	case CacheVolumeID{}.ResourceTypeName():
		return CacheVolumeID{ID: id}.Decode()
	case DirectoryID{}.ResourceTypeName():
		return DirectoryID{ID: id}.Decode()
	case FileID{}.ResourceTypeName():
		return FileID{ID: id}.Decode()
	case SecretID{}.ResourceTypeName():
		return SecretID{ID: id}.Decode()
	case ServiceID{}.ResourceTypeName():
		return ServiceID{ID: id}.Decode()
	case resourceid.ID[Module]{}.ResourceTypeName():
		return ModuleID{ID: id}.Decode()
	case FunctionID{}.ResourceTypeName():
		return FunctionID{ID: id}.Decode()
	case socket.ID{}.ResourceTypeName():
		return socket.ID{ID: id}.Decode()
	case TypeDefID{}.ResourceTypeName():
		return TypeDefID{ID: id}.Decode()
	case GeneratedCodeID{}.ResourceTypeName():
		return GeneratedCodeID{ID: id}.Decode()
	}
	return nil, fmt.Errorf("unknown resource type: %v", id.TypeName)
}

// ResourceToID returns the ID string corresponding to the given resource.
func ResourceToID(r any) (*idproto.ID, error) {
	var id *idproto.ID
	switch r := r.(type) {
	case *Container:
		id = r.ID
	case *CacheVolume:
		id = r.ID
	case *Directory:
		id = r.ID
	case *File:
		id = r.ID
	case *Secret:
		id = r.ID
	case *Service:
		id = r.ID
	case *Module:
		id = r.ID
	case *Function:
		id = r.ID
	case *socket.Socket:
		id = r.ID
	case *TypeDef:
		id = r.ID
	case *GeneratedCode:
		id = r.ID
	default:
		return nil, fmt.Errorf("unknown resource type: %T", r)
	}
	if id == nil {
		return nil, fmt.Errorf("%T has a null ID", r) // TODO this might be valid
	}
	return id, nil
}
