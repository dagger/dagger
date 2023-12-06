package core

import (
	"github.com/dagger/dagger/core/resourceid"
)

type ContainerID = resourceid.ID[Container]

type ServiceID = resourceid.ID[Service]

type CacheVolumeID = resourceid.ID[CacheVolume]

type DirectoryID = resourceid.ID[Directory]

type FileID = resourceid.ID[File]

type SecretID = resourceid.ID[Secret]

type ModuleID = resourceid.ID[Module]

type FunctionID = resourceid.ID[Function]

type FunctionArgID = resourceid.ID[FunctionArg]

type TypeDefID = resourceid.ID[TypeDef]

type GeneratedCodeID = resourceid.ID[GeneratedCode]

type GitRepositoryID = resourceid.ID[GitRepository]

type GitRefID = resourceid.ID[GitRef]

// SocketID is in the socket package (to avoid circular imports)
