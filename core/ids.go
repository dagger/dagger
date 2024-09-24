package core

import (
	"github.com/dagger/dagger/dagql"
)

type ContainerID = dagql.ID[*Container]

type ServiceID = dagql.ID[*Service]

type CacheVolumeID = dagql.ID[*CacheVolume]

type DirectoryID = dagql.ID[*Directory]

type FileID = dagql.ID[*File]

type SecretID = dagql.ID[*Secret]

type ModuleID = dagql.ID[*Module]

type ModuleDependencyID = dagql.ID[*ModuleDependency]

type ModuleSourceID = dagql.ID[*ModuleSource]

type LocalModuleSourceID = dagql.ID[*LocalModuleSource]

type GitModuleSourceID = dagql.ID[*GitModuleSource]

type FunctionID = dagql.ID[*Function]

type FunctionArgID = dagql.ID[*FunctionArg]

type TypeDefID = dagql.ID[*TypeDef]

type SourceMapID = dagql.ID[*SourceMap]

type GeneratedCodeID = dagql.ID[*GeneratedCode]

type GitRepositoryID = dagql.ID[*GitRepository]

type GitRefID = dagql.ID[*GitRef]

type SocketID = dagql.ID[*Socket]
