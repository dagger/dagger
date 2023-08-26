package core

import (
	"fmt"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/core/socket"
)

type ContainerID = resourceid.ID[Container]

type CacheID = resourceid.ID[CacheVolume]

type DirectoryID = resourceid.ID[Directory]

type FileID = resourceid.ID[File]

type SecretID = resourceid.ID[Secret]

type EnvironmentID resourceid.ID[Environment]

type CheckID = resourceid.ID[Check]

type CheckResultID = resourceid.ID[CheckResult]

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
	case CacheID.ResourceTypeName(""):
		return CacheID(id).Decode()
	case DirectoryID.ResourceTypeName(""):
		return DirectoryID(id).Decode()
	case FileID.ResourceTypeName(""):
		return FileID(id).Decode()
	case SecretID.ResourceTypeName(""):
		return SecretID(id).Decode()
	case resourceid.ID[Environment].ResourceTypeName(""):
		return EnvironmentID(id).Decode()
	case CheckID.ResourceTypeName(""):
		return CheckID(id).Decode()
	case CheckResultID.ResourceTypeName(""):
		return CheckResultID(id).Decode()
	case socket.ID.ResourceTypeName(""):
		return socket.ID(id).Decode()
	}
	return nil, fmt.Errorf("unknown resource type: %v", typeName)
}
