package core

import (
	"github.com/dagger/dagger/dagql"
)

type JSONValueID = dagql.ID[*JSONValue]

type AddressID = dagql.ID[*Address]

type ContainerID = dagql.ID[*Container]

type ServiceID = dagql.ID[*Service]

type CacheVolumeID = dagql.ID[*CacheVolume]

type VolumeID = dagql.ID[*Volume]

type DirectoryID = dagql.ID[*Directory]

type FileID = dagql.ID[*File]

type SecretID = dagql.ID[*Secret]

type ModuleID = dagql.ID[*Module]

type ModuleSourceID = dagql.ID[*ModuleSource]

type FunctionID = dagql.ID[*Function]

type FunctionArgID = dagql.ID[*FunctionArg]

type TypeDefID = dagql.ID[*TypeDef]

type SourceMapID = dagql.ID[*SourceMap]

type GeneratedCodeID = dagql.ID[*GeneratedCode]

type GitRepositoryID = dagql.ID[*GitRepository]

type GitRefID = dagql.ID[*GitRef]

type SocketID = dagql.ID[*Socket]

type LLMID = dagql.ID[*LLM]

type EnvID = dagql.ID[*Env]
