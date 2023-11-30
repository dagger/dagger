package core

import (
	"fmt"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/core/socket"
)

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
func ResourceFromID(id string) (any, error) {
	typeName, err := resourceid.TypeName(id)
	if err != nil {
		return nil, err
	}
	switch typeName {
	case ContainerID.ResourceTypeName(""):
		return ContainerID(id).Decode()
	case CacheVolumeID.ResourceTypeName(""):
		return CacheVolumeID(id).Decode()
	case DirectoryID.ResourceTypeName(""):
		return DirectoryID(id).Decode()
	case FileID.ResourceTypeName(""):
		return FileID(id).Decode()
	case SecretID.ResourceTypeName(""):
		return SecretID(id).Decode()
	case ServiceID.ResourceTypeName(""):
		return ServiceID(id).Decode()
	case resourceid.ID[Module].ResourceTypeName(""):
		return ModuleID(id).Decode()
	case FunctionID.ResourceTypeName(""):
		return FunctionID(id).Decode()
	case socket.ID.ResourceTypeName(""):
		return socket.ID(id).Decode()
	case TypeDefID.ResourceTypeName(""):
		return TypeDefID(id).Decode()
	case GeneratedCodeID.ResourceTypeName(""):
		return GeneratedCodeID(id).Decode()
	}
	return nil, fmt.Errorf("unknown resource type: %v", typeName)
}

// ResourceToID returns the ID string corresponding to the given resource.
func ResourceToID(r any) (string, error) {
	var id fmt.Stringer
	var err error
	switch r := r.(type) {
	case *Container:
		id, err = r.ID()
	case ContainerID:
		return string(r), nil
	case *CacheVolume:
		id, err = r.ID()
	case CacheVolumeID:
		return string(r), nil
	case *Directory:
		id, err = r.ID()
	case DirectoryID:
		return string(r), nil
	case *File:
		id, err = r.ID()
	case FileID:
		return string(r), nil
	case *Secret:
		id, err = r.ID()
	case SecretID:
		return string(r), nil
	case *Service:
		id, err = r.ID()
	case ServiceID:
		return string(r), nil
	case *Module:
		id, err = r.ID()
	case ModuleID:
		return string(r), nil
	case *Function:
		id, err = r.ID()
	case FunctionID:
		return string(r), nil
	case *socket.Socket:
		id, err = r.ID()
	case socket.ID:
		return string(r), nil
	case *TypeDef:
		id, err = r.ID()
	case TypeDefID:
		return string(r), nil
	case *GeneratedCode:
		id, err = r.ID()
	case GeneratedCodeID:
		return string(r), nil
	default:
		return "", fmt.Errorf("unknown resource type: %T", r)
	}
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
