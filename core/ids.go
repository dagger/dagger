package core

import (
	"github.com/dagger/dagger/core/idproto"
	"github.com/dagger/dagger/core/resourceid"
)

// Object is an immutable value in the DAG that can be referred to by an ID
// and cloned.
//
// Within a Dagger session the ID will always resolve to the same Object by
// means of a session-local cache.
//
// Objects are identified by their ID, which also doubles as a serializable
// constructor for the object. By default, an object's ID will be the query
// that led to it. A GraphQL resolver may choose to set an ID of its own, for
// example to return a reproducible object from a moving reference.
type Object[T any] interface {
	IDable
	Cloneable[T]
}

// Identified is embedded in an Object to make it IDable.
type Identified struct {
	id *idproto.ID
}

func (i *Identified) ID() *idproto.ID {
	if i == nil {
		return nil
	}
	return i.id
}

func (i *Identified) SetID(id *idproto.ID) {
	i.id = id
}

func (i *Identified) Reset() {
	i.id = nil
}

// IDable is an object that has an ID.
type IDable interface {
	ID() *idproto.ID
	SetID(*idproto.ID)
}

// Cloneable an object that can return a mutable copy of itself that will not
// modify the original object.
type Cloneable[T any] interface {
	Clone() T
}

type ContainerID = *resourceid.ID[*Container]

type ServiceID = *resourceid.ID[*Service]

type CacheVolumeID = *resourceid.ID[*CacheVolume]

type DirectoryID = *resourceid.ID[*Directory]

type FileID = *resourceid.ID[*File]

type SecretID = *resourceid.ID[*Secret]

type ModuleID = *resourceid.ID[*Module]

type FunctionID = *resourceid.ID[*Function]

type TypeDefID = *resourceid.ID[*TypeDef]

type GeneratedCodeID = *resourceid.ID[*GeneratedCode]

type SocketID = *resourceid.ID[*Socket]
