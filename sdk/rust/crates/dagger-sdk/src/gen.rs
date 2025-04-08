#![allow(clippy::needless_lifetimes)]

use crate::core::cli_session::DaggerSessionProc;
use crate::core::graphql_client::DynGraphQLClient;
use crate::errors::DaggerError;
use crate::id::IntoID;
use crate::querybuilder::Selection;
use derive_builder::Builder;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct BindingId(pub String);
impl From<&str> for BindingId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for BindingId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<BindingId> for Binding {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<BindingId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<BindingId> for BindingId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<BindingId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<BindingId, DaggerError>(self) })
    }
}
impl BindingId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct CacheVolumeId(pub String);
impl From<&str> for CacheVolumeId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for CacheVolumeId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<CacheVolumeId> for CacheVolume {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<CacheVolumeId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<CacheVolumeId> for CacheVolumeId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<CacheVolumeId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<CacheVolumeId, DaggerError>(self) })
    }
}
impl CacheVolumeId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ContainerId(pub String);
impl From<&str> for ContainerId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ContainerId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ContainerId> for Container {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ContainerId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ContainerId> for ContainerId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ContainerId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ContainerId, DaggerError>(self) })
    }
}
impl ContainerId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct CurrentModuleId(pub String);
impl From<&str> for CurrentModuleId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for CurrentModuleId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<CurrentModuleId> for CurrentModule {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<CurrentModuleId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<CurrentModuleId> for CurrentModuleId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<CurrentModuleId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<CurrentModuleId, DaggerError>(self) })
    }
}
impl CurrentModuleId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct DirectoryId(pub String);
impl From<&str> for DirectoryId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for DirectoryId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<DirectoryId> for Directory {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<DirectoryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<DirectoryId> for DirectoryId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<DirectoryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<DirectoryId, DaggerError>(self) })
    }
}
impl DirectoryId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EngineCacheEntryId(pub String);
impl From<&str> for EngineCacheEntryId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EngineCacheEntryId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EngineCacheEntryId> for EngineCacheEntry {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheEntryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EngineCacheEntryId> for EngineCacheEntryId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheEntryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EngineCacheEntryId, DaggerError>(self) })
    }
}
impl EngineCacheEntryId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EngineCacheEntrySetId(pub String);
impl From<&str> for EngineCacheEntrySetId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EngineCacheEntrySetId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EngineCacheEntrySetId> for EngineCacheEntrySet {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheEntrySetId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EngineCacheEntrySetId> for EngineCacheEntrySetId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheEntrySetId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EngineCacheEntrySetId, DaggerError>(self) })
    }
}
impl EngineCacheEntrySetId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EngineCacheId(pub String);
impl From<&str> for EngineCacheId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EngineCacheId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EngineCacheId> for EngineCache {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EngineCacheId> for EngineCacheId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EngineCacheId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EngineCacheId, DaggerError>(self) })
    }
}
impl EngineCacheId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EngineId(pub String);
impl From<&str> for EngineId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EngineId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EngineId> for Engine {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<EngineId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EngineId> for EngineId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<EngineId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<EngineId, DaggerError>(self) })
    }
}
impl EngineId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EnumTypeDefId(pub String);
impl From<&str> for EnumTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EnumTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EnumTypeDefId> for EnumTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnumTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EnumTypeDefId> for EnumTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnumTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EnumTypeDefId, DaggerError>(self) })
    }
}
impl EnumTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EnumValueTypeDefId(pub String);
impl From<&str> for EnumValueTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EnumValueTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EnumValueTypeDefId> for EnumValueTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnumValueTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EnumValueTypeDefId> for EnumValueTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnumValueTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EnumValueTypeDefId, DaggerError>(self) })
    }
}
impl EnumValueTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EnvId(pub String);
impl From<&str> for EnvId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EnvId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EnvId> for Env {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<EnvId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EnvId> for EnvId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<EnvId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<EnvId, DaggerError>(self) })
    }
}
impl EnvId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct EnvVariableId(pub String);
impl From<&str> for EnvVariableId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for EnvVariableId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<EnvVariableId> for EnvVariable {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnvVariableId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<EnvVariableId> for EnvVariableId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<EnvVariableId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<EnvVariableId, DaggerError>(self) })
    }
}
impl EnvVariableId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ErrorId(pub String);
impl From<&str> for ErrorId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ErrorId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ErrorId> for Error {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ErrorId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ErrorId> for ErrorId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ErrorId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<ErrorId, DaggerError>(self) })
    }
}
impl ErrorId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ErrorValueId(pub String);
impl From<&str> for ErrorValueId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ErrorValueId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ErrorValueId> for ErrorValue {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ErrorValueId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ErrorValueId> for ErrorValueId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ErrorValueId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ErrorValueId, DaggerError>(self) })
    }
}
impl ErrorValueId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FieldTypeDefId(pub String);
impl From<&str> for FieldTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FieldTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FieldTypeDefId> for FieldTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FieldTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FieldTypeDefId> for FieldTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FieldTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<FieldTypeDefId, DaggerError>(self) })
    }
}
impl FieldTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FileId(pub String);
impl From<&str> for FileId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FileId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FileId> for File {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<FileId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FileId> for FileId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<FileId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<FileId, DaggerError>(self) })
    }
}
impl FileId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionArgId(pub String);
impl From<&str> for FunctionArgId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FunctionArgId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FunctionArgId> for FunctionArg {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionArgId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FunctionArgId> for FunctionArgId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionArgId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<FunctionArgId, DaggerError>(self) })
    }
}
impl FunctionArgId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionCallArgValueId(pub String);
impl From<&str> for FunctionCallArgValueId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FunctionCallArgValueId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FunctionCallArgValueId> for FunctionCallArgValue {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionCallArgValueId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FunctionCallArgValueId> for FunctionCallArgValueId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionCallArgValueId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<FunctionCallArgValueId, DaggerError>(self) })
    }
}
impl FunctionCallArgValueId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionCallId(pub String);
impl From<&str> for FunctionCallId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FunctionCallId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FunctionCallId> for FunctionCall {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionCallId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FunctionCallId> for FunctionCallId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<FunctionCallId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<FunctionCallId, DaggerError>(self) })
    }
}
impl FunctionCallId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionId(pub String);
impl From<&str> for FunctionId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for FunctionId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<FunctionId> for Function {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<FunctionId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<FunctionId> for FunctionId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<FunctionId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<FunctionId, DaggerError>(self) })
    }
}
impl FunctionId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GeneratedCodeId(pub String);
impl From<&str> for GeneratedCodeId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for GeneratedCodeId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<GeneratedCodeId> for GeneratedCode {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<GeneratedCodeId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<GeneratedCodeId> for GeneratedCodeId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<GeneratedCodeId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<GeneratedCodeId, DaggerError>(self) })
    }
}
impl GeneratedCodeId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GitRefId(pub String);
impl From<&str> for GitRefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for GitRefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<GitRefId> for GitRef {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<GitRefId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<GitRefId> for GitRefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<GitRefId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<GitRefId, DaggerError>(self) })
    }
}
impl GitRefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GitRepositoryId(pub String);
impl From<&str> for GitRepositoryId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for GitRepositoryId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<GitRepositoryId> for GitRepository {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<GitRepositoryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<GitRepositoryId> for GitRepositoryId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<GitRepositoryId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<GitRepositoryId, DaggerError>(self) })
    }
}
impl GitRepositoryId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct HostId(pub String);
impl From<&str> for HostId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for HostId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<HostId> for Host {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<HostId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<HostId> for HostId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<HostId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<HostId, DaggerError>(self) })
    }
}
impl HostId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct InputTypeDefId(pub String);
impl From<&str> for InputTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for InputTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<InputTypeDefId> for InputTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<InputTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<InputTypeDefId> for InputTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<InputTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<InputTypeDefId, DaggerError>(self) })
    }
}
impl InputTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct InterfaceTypeDefId(pub String);
impl From<&str> for InterfaceTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for InterfaceTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<InterfaceTypeDefId> for InterfaceTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<InterfaceTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<InterfaceTypeDefId> for InterfaceTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<InterfaceTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<InterfaceTypeDefId, DaggerError>(self) })
    }
}
impl InterfaceTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Json(pub String);
impl From<&str> for Json {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for Json {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl Json {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Llmid(pub String);
impl From<&str> for Llmid {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for Llmid {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<Llmid> for Llm {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<Llmid, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<Llmid> for Llmid {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<Llmid, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<Llmid, DaggerError>(self) })
    }
}
impl Llmid {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct LlmTokenUsageId(pub String);
impl From<&str> for LlmTokenUsageId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for LlmTokenUsageId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<LlmTokenUsageId> for LlmTokenUsage {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<LlmTokenUsageId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<LlmTokenUsageId> for LlmTokenUsageId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<LlmTokenUsageId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<LlmTokenUsageId, DaggerError>(self) })
    }
}
impl LlmTokenUsageId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct LabelId(pub String);
impl From<&str> for LabelId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for LabelId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<LabelId> for Label {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<LabelId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<LabelId> for LabelId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<LabelId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<LabelId, DaggerError>(self) })
    }
}
impl LabelId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ListTypeDefId(pub String);
impl From<&str> for ListTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ListTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ListTypeDefId> for ListTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ListTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ListTypeDefId> for ListTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ListTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ListTypeDefId, DaggerError>(self) })
    }
}
impl ListTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ModuleConfigClientId(pub String);
impl From<&str> for ModuleConfigClientId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ModuleConfigClientId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ModuleConfigClientId> for ModuleConfigClient {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ModuleConfigClientId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ModuleConfigClientId> for ModuleConfigClientId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ModuleConfigClientId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ModuleConfigClientId, DaggerError>(self) })
    }
}
impl ModuleConfigClientId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ModuleId(pub String);
impl From<&str> for ModuleId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ModuleId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ModuleId> for Module {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ModuleId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ModuleId> for ModuleId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ModuleId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<ModuleId, DaggerError>(self) })
    }
}
impl ModuleId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ModuleSourceId(pub String);
impl From<&str> for ModuleSourceId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ModuleSourceId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ModuleSourceId> for ModuleSource {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ModuleSourceId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ModuleSourceId> for ModuleSourceId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ModuleSourceId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ModuleSourceId, DaggerError>(self) })
    }
}
impl ModuleSourceId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ObjectTypeDefId(pub String);
impl From<&str> for ObjectTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ObjectTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ObjectTypeDefId> for ObjectTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ObjectTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ObjectTypeDefId> for ObjectTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ObjectTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ObjectTypeDefId, DaggerError>(self) })
    }
}
impl ObjectTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Platform(pub String);
impl From<&str> for Platform {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for Platform {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl Platform {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct PortId(pub String);
impl From<&str> for PortId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for PortId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<PortId> for Port {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<PortId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<PortId> for PortId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<PortId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<PortId, DaggerError>(self) })
    }
}
impl PortId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SdkConfigId(pub String);
impl From<&str> for SdkConfigId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for SdkConfigId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<SdkConfigId> for SdkConfig {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<SdkConfigId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<SdkConfigId> for SdkConfigId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<SdkConfigId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<SdkConfigId, DaggerError>(self) })
    }
}
impl SdkConfigId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ScalarTypeDefId(pub String);
impl From<&str> for ScalarTypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ScalarTypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ScalarTypeDefId> for ScalarTypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ScalarTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ScalarTypeDefId> for ScalarTypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<ScalarTypeDefId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<ScalarTypeDefId, DaggerError>(self) })
    }
}
impl ScalarTypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SecretId(pub String);
impl From<&str> for SecretId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for SecretId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<SecretId> for Secret {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<SecretId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<SecretId> for SecretId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<SecretId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<SecretId, DaggerError>(self) })
    }
}
impl SecretId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ServiceId(pub String);
impl From<&str> for ServiceId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for ServiceId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<ServiceId> for Service {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ServiceId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<ServiceId> for ServiceId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<ServiceId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<ServiceId, DaggerError>(self) })
    }
}
impl ServiceId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SocketId(pub String);
impl From<&str> for SocketId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for SocketId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<SocketId> for Socket {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<SocketId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<SocketId> for SocketId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<SocketId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<SocketId, DaggerError>(self) })
    }
}
impl SocketId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SourceMapId(pub String);
impl From<&str> for SourceMapId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for SourceMapId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<SourceMapId> for SourceMap {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<SourceMapId, DaggerError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<SourceMapId> for SourceMapId {
    fn into_id(
        self,
    ) -> std::pin::Pin<
        Box<dyn core::future::Future<Output = Result<SourceMapId, DaggerError>> + Send>,
    > {
        Box::pin(async move { Ok::<SourceMapId, DaggerError>(self) })
    }
}
impl SourceMapId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct TerminalId(pub String);
impl From<&str> for TerminalId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for TerminalId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<TerminalId> for Terminal {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<TerminalId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<TerminalId> for TerminalId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<TerminalId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<TerminalId, DaggerError>(self) })
    }
}
impl TerminalId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct TypeDefId(pub String);
impl From<&str> for TypeDefId {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for TypeDefId {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl IntoID<TypeDefId> for TypeDef {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<TypeDefId, DaggerError>> + Send>>
    {
        Box::pin(async move { self.id().await })
    }
}
impl IntoID<TypeDefId> for TypeDefId {
    fn into_id(
        self,
    ) -> std::pin::Pin<Box<dyn core::future::Future<Output = Result<TypeDefId, DaggerError>> + Send>>
    {
        Box::pin(async move { Ok::<TypeDefId, DaggerError>(self) })
    }
}
impl TypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Void(pub String);
impl From<&str> for Void {
    fn from(value: &str) -> Self {
        Self(value.to_string())
    }
}
impl From<String> for Void {
    fn from(value: String) -> Self {
        Self(value)
    }
}
impl Void {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
pub struct BuildArg {
    pub name: String,
    pub value: String,
}
#[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
pub struct PipelineLabel {
    pub name: String,
    pub value: String,
}
#[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
pub struct PortForward {
    pub backend: isize,
    pub frontend: isize,
    pub protocol: NetworkProtocol,
}
#[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
pub struct SecretArg {
    pub name: String,
    pub value: SecretId,
}
#[derive(Clone)]
pub struct Binding {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Binding {
    /// Retrieve the binding value, as type CacheVolume
    pub fn as_cache_volume(&self) -> CacheVolume {
        let query = self.selection.select("asCacheVolume");
        CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Container
    pub fn as_container(&self) -> Container {
        let query = self.selection.select("asContainer");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Directory
    pub fn as_directory(&self) -> Directory {
        let query = self.selection.select("asDirectory");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Env
    pub fn as_env(&self) -> Env {
        let query = self.selection.select("asEnv");
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type File
    pub fn as_file(&self) -> File {
        let query = self.selection.select("asFile");
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type GitRef
    pub fn as_git_ref(&self) -> GitRef {
        let query = self.selection.select("asGitRef");
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type GitRepository
    pub fn as_git_repository(&self) -> GitRepository {
        let query = self.selection.select("asGitRepository");
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type LLM
    pub fn as_llm(&self) -> Llm {
        let query = self.selection.select("asLLM");
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Module
    pub fn as_module(&self) -> Module {
        let query = self.selection.select("asModule");
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type ModuleConfigClient
    pub fn as_module_config_client(&self) -> ModuleConfigClient {
        let query = self.selection.select("asModuleConfigClient");
        ModuleConfigClient {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type ModuleSource
    pub fn as_module_source(&self) -> ModuleSource {
        let query = self.selection.select("asModuleSource");
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Secret
    pub fn as_secret(&self) -> Secret {
        let query = self.selection.select("asSecret");
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Service
    pub fn as_service(&self) -> Service {
        let query = self.selection.select("asService");
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieve the binding value, as type Socket
    pub fn as_socket(&self) -> Socket {
        let query = self.selection.select("asSocket");
        Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The digest of the binding value
    pub async fn digest(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("digest");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this Binding.
    pub async fn id(&self) -> Result<BindingId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The binding name
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The binding type
    pub async fn type_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("typeName");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct CacheVolume {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl CacheVolume {
    /// A unique identifier for this CacheVolume.
    pub async fn id(&self) -> Result<CacheVolumeId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Container {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerAsServiceOpts<'a> {
    /// Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    /// If empty, the container's default command is used.
    #[builder(setter(into, strip_option), default)]
    pub args: Option<Vec<&'a str>>,
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
    /// If set, skip the automatic init process injected into containers by default.
    /// This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    #[builder(setter(into, strip_option), default)]
    pub no_init: Option<bool>,
    /// If the container has an entrypoint, prepend it to the args.
    #[builder(setter(into, strip_option), default)]
    pub use_entrypoint: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerAsTarballOpts {
    /// Force each layer of the image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the image's layers.
    /// Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform images.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerBuildOpts<'a> {
    /// Additional build arguments.
    #[builder(setter(into, strip_option), default)]
    pub build_args: Option<Vec<BuildArg>>,
    /// Path to the Dockerfile to use.
    #[builder(setter(into, strip_option), default)]
    pub dockerfile: Option<&'a str>,
    /// If set, skip the automatic init process injected into containers created by RUN statements.
    /// This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    #[builder(setter(into, strip_option), default)]
    pub no_init: Option<bool>,
    /// Secrets to pass to the build.
    /// They will be mounted at /run/secrets/[secret-name] in the build container
    /// They can be accessed in the Dockerfile using the "secret" mount type and mount path /run/secrets/[secret-name], e.g. RUN --mount=type=secret,id=my-secret curl [http://example.com?token=$(cat /run/secrets/my-secret)](http://example.com?token=$(cat /run/secrets/my-secret))
    #[builder(setter(into, strip_option), default)]
    pub secret_args: Option<Vec<SecretArg>>,
    /// Target build stage to build.
    #[builder(setter(into, strip_option), default)]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerDirectoryOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExportOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Force each layer of the exported image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the exported image's layers.
    /// Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerFileOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerImportOpts<'a> {
    /// Identifies the tag to import from the archive, if the archive bundles multiple tags.
    #[builder(setter(into, strip_option), default)]
    pub tag: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerPublishOpts {
    /// Force each layer of the published image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the published image's layers.
    /// Defaults to OCI, which is largely compatible with most recent registries, but Docker may be needed for older registries without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerTerminalOpts<'a> {
    /// If set, override the container's default terminal command and invoke these command arguments instead.
    #[builder(setter(into, strip_option), default)]
    pub cmd: Option<Vec<&'a str>>,
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerUpOpts<'a> {
    /// Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    /// If empty, the container's default command is used.
    #[builder(setter(into, strip_option), default)]
    pub args: Option<Vec<&'a str>>,
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
    /// If set, skip the automatic init process injected into containers by default.
    /// This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    #[builder(setter(into, strip_option), default)]
    pub no_init: Option<bool>,
    /// List of frontend/backend port mappings to forward.
    /// Frontend is the port accepting traffic on the host, backend is the service port.
    #[builder(setter(into, strip_option), default)]
    pub ports: Option<Vec<PortForward>>,
    /// Bind each tunnel port to a random port on the host.
    #[builder(setter(into, strip_option), default)]
    pub random: Option<bool>,
    /// If the container has an entrypoint, prepend it to the args.
    #[builder(setter(into, strip_option), default)]
    pub use_entrypoint: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDefaultTerminalCmdOpts {
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDirectoryOpts<'a> {
    /// Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).
    #[builder(setter(into, strip_option), default)]
    pub include: Option<Vec<&'a str>>,
    /// A user:group to set for the directory and its contents.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithEntrypointOpts {
    /// Don't remove the default arguments when setting the entrypoint.
    #[builder(setter(into, strip_option), default)]
    pub keep_default_args: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithEnvVariableOpts {
    /// Replace "${VAR}" or "$VAR" in the value according to the current environment variables defined in the container (e.g. "/opt/bin:$PATH").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithExecOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Exit codes this command is allowed to exit with without error
    #[builder(setter(into, strip_option), default)]
    pub expect: Option<ReturnType>,
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
    /// If set, skip the automatic init process injected into containers by default.
    /// This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    #[builder(setter(into, strip_option), default)]
    pub no_init: Option<bool>,
    /// Redirect the command's standard error to a file in the container (e.g., "/tmp/stderr").
    #[builder(setter(into, strip_option), default)]
    pub redirect_stderr: Option<&'a str>,
    /// Redirect the command's standard output to a file in the container (e.g., "/tmp/stdout").
    #[builder(setter(into, strip_option), default)]
    pub redirect_stdout: Option<&'a str>,
    /// Content to write to the command's standard input before closing (e.g., "Hello world").
    #[builder(setter(into, strip_option), default)]
    pub stdin: Option<&'a str>,
    /// If the container has an entrypoint, prepend it to the args.
    #[builder(setter(into, strip_option), default)]
    pub use_entrypoint: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithExposedPortOpts<'a> {
    /// Optional port description
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Skip the health check when run as a service.
    #[builder(setter(into, strip_option), default)]
    pub experimental_skip_healthcheck: Option<bool>,
    /// Transport layer network protocol
    #[builder(setter(into, strip_option), default)]
    pub protocol: Option<NetworkProtocol>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithFileOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Permission given to the copied file (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithFilesOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the files.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Permission given to the copied files (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedCacheOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the mounted cache directory.
    /// Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Sharing mode of the cache volume.
    #[builder(setter(into, strip_option), default)]
    pub sharing: Option<CacheSharingMode>,
    /// Identifier of the directory to use as the cache volume's root.
    #[builder(setter(into, strip_option), default)]
    pub source: Option<DirectoryId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedDirectoryOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the mounted directory and its contents.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedFileOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user or user:group to set for the mounted file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedSecretOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Permission given to the mounted secret (e.g., 0600).
    /// This option requires an owner to be set to be active.
    #[builder(setter(into, strip_option), default)]
    pub mode: Option<isize>,
    /// A user:group to set for the mounted secret.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedTempOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// Size of the temporary directory in bytes.
    #[builder(setter(into, strip_option), default)]
    pub size: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithNewFileOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Permission given to the written file (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithUnixSocketOpts<'a> {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
    /// A user:group to set for the mounted socket.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithWorkdirOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutDirectoryOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutEntrypointOpts {
    /// Don't remove the default arguments when unsetting the entrypoint.
    #[builder(setter(into, strip_option), default)]
    pub keep_default_args: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutExposedPortOpts {
    /// Port protocol to unexpose
    #[builder(setter(into, strip_option), default)]
    pub protocol: Option<NetworkProtocol>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutFileOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutFilesOpts {
    /// Replace "${VAR}" or "$VAR" in the value of paths according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutMountOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithoutUnixSocketOpts {
    /// Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
impl Container {
    /// Turn the container into a Service.
    /// Be sure to set any exposed ports before this conversion.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_service(&self) -> Service {
        let query = self.selection.select("asService");
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Turn the container into a Service.
    /// Be sure to set any exposed ports before this conversion.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_service_opts<'a>(&self, opts: ContainerAsServiceOpts<'a>) -> Service {
        let mut query = self.selection.select("asService");
        if let Some(args) = opts.args {
            query = query.arg("args", args);
        }
        if let Some(use_entrypoint) = opts.use_entrypoint {
            query = query.arg("useEntrypoint", use_entrypoint);
        }
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        if let Some(no_init) = opts.no_init {
            query = query.arg("noInit", no_init);
        }
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a File representing the container serialized to a tarball.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_tarball(&self) -> File {
        let query = self.selection.select("asTarball");
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a File representing the container serialized to a tarball.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_tarball_opts(&self, opts: ContainerAsTarballOpts) -> File {
        let mut query = self.selection.select("asTarball");
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        if let Some(forced_compression) = opts.forced_compression {
            query = query.arg("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg("mediaTypes", media_types);
        }
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initializes this container from a Dockerfile build.
    ///
    /// # Arguments
    ///
    /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build(&self, context: impl IntoID<DirectoryId>) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg_lazy(
            "context",
            Box::new(move || {
                let context = context.clone();
                Box::pin(async move { context.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initializes this container from a Dockerfile build.
    ///
    /// # Arguments
    ///
    /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build_opts<'a>(
        &self,
        context: impl IntoID<DirectoryId>,
        opts: ContainerBuildOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg_lazy(
            "context",
            Box::new(move || {
                let context = context.clone();
                Box::pin(async move { context.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(dockerfile) = opts.dockerfile {
            query = query.arg("dockerfile", dockerfile);
        }
        if let Some(target) = opts.target {
            query = query.arg("target", target);
        }
        if let Some(build_args) = opts.build_args {
            query = query.arg("buildArgs", build_args);
        }
        if let Some(secret_args) = opts.secret_args {
            query = query.arg("secretArgs", secret_args);
        }
        if let Some(no_init) = opts.no_init {
            query = query.arg("noInit", no_init);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves default arguments for future commands.
    pub async fn default_args(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("defaultArgs");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves a directory at the given path.
    /// Mounts are included.
    ///
    /// # Arguments
    ///
    /// * `path` - The path of the directory to retrieve (e.g., "./src").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves a directory at the given path.
    /// Mounts are included.
    ///
    /// # Arguments
    ///
    /// * `path` - The path of the directory to retrieve (e.g., "./src").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerDirectoryOpts,
    ) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves entrypoint to be prepended to the arguments of all commands.
    pub async fn entrypoint(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("entrypoint");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the value of the specified environment variable.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable to retrieve (e.g., "PATH").
    pub async fn env_variable(&self, name: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("envVariable");
        query = query.arg("name", name.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of environment variables passed to commands.
    pub fn env_variables(&self) -> Vec<EnvVariable> {
        let query = self.selection.select("envVariables");
        vec![EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The exit code of the last executed command.
    /// Returns an error if no command was set.
    pub async fn exit_code(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("exitCode");
        query.execute(self.graphql_client.clone()).await
    }
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    /// Configures all available GPUs on the host to be accessible to this container.
    /// This currently works for Nvidia devices only.
    pub fn experimental_with_all_gp_us(&self) -> Container {
        let query = self.selection.select("experimentalWithAllGPUs");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    /// Configures the provided list of devices to be accessible to this container.
    /// This currently works for Nvidia devices only.
    ///
    /// # Arguments
    ///
    /// * `devices` - List of devices to be accessible to this container.
    pub fn experimental_with_gpu(&self, devices: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("experimentalWithGPU");
        query = query.arg(
            "devices",
            devices
                .into_iter()
                .map(|i| i.into())
                .collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Writes the container as an OCI tarball to the destination file path on the host.
    /// It can also export platform variants.
    ///
    /// # Arguments
    ///
    /// * `path` - Host's destination path (e.g., "./tarball").
    ///
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the container as an OCI tarball to the destination file path on the host.
    /// It can also export platform variants.
    ///
    /// # Arguments
    ///
    /// * `path` - Host's destination path (e.g., "./tarball").
    ///
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerExportOpts,
    ) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        if let Some(forced_compression) = opts.forced_compression {
            query = query.arg("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg("mediaTypes", media_types);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of exposed ports.
    /// This includes ports already exposed by the image, even if not explicitly added with dagger.
    pub fn exposed_ports(&self) -> Vec<Port> {
        let query = self.selection.select("exposedPorts");
        vec![Port {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Retrieves a file at the given path.
    /// Mounts are included.
    ///
    /// # Arguments
    ///
    /// * `path` - The path of the file to retrieve (e.g., "./README.md").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves a file at the given path.
    /// Mounts are included.
    ///
    /// # Arguments
    ///
    /// * `path` - The path of the file to retrieve (e.g., "./README.md").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn file_opts(&self, path: impl Into<String>, opts: ContainerFileOpts) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initializes this container from a pulled base image.
    ///
    /// # Arguments
    ///
    /// * `address` - Image's address from its registry.
    ///
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g., "docker.io/dagger/dagger:main").
    pub fn from(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("from");
        query = query.arg("address", address.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this Container.
    pub async fn id(&self) -> Result<ContainerId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The unique image reference which can only be retrieved immediately after the 'Container.From' call.
    pub async fn image_ref(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("imageRef");
        query.execute(self.graphql_client.clone()).await
    }
    /// Reads the container from an OCI tarball.
    ///
    /// # Arguments
    ///
    /// * `source` - File to read the container from.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn import(&self, source: impl IntoID<FileId>) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Reads the container from an OCI tarball.
    ///
    /// # Arguments
    ///
    /// * `source` - File to read the container from.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn import_opts<'a>(
        &self,
        source: impl IntoID<FileId>,
        opts: ContainerImportOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(tag) = opts.tag {
            query = query.arg("tag", tag);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves the value of the specified label.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the label (e.g., "org.opencontainers.artifact.created").
    pub async fn label(&self, name: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("label");
        query = query.arg("name", name.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of labels passed to container.
    pub fn labels(&self) -> Vec<Label> {
        let query = self.selection.select("labels");
        vec![Label {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Retrieves the list of paths where a directory is mounted.
    pub async fn mounts(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("mounts");
        query.execute(self.graphql_client.clone()).await
    }
    /// The platform this container executes and publishes as.
    pub async fn platform(&self) -> Result<Platform, DaggerError> {
        let query = self.selection.select("platform");
        query.execute(self.graphql_client.clone()).await
    }
    /// Publishes this container as a new image to the specified address.
    /// Publish returns a fully qualified ref.
    /// It can also publish platform variants.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to publish the image to.
    ///
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. "docker.io/dagger/dagger:main").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn publish(&self, address: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("publish");
        query = query.arg("address", address.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Publishes this container as a new image to the specified address.
    /// Publish returns a fully qualified ref.
    /// It can also publish platform variants.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to publish the image to.
    ///
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. "docker.io/dagger/dagger:main").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn publish_opts(
        &self,
        address: impl Into<String>,
        opts: ContainerPublishOpts,
    ) -> Result<String, DaggerError> {
        let mut query = self.selection.select("publish");
        query = query.arg("address", address.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        if let Some(forced_compression) = opts.forced_compression {
            query = query.arg("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg("mediaTypes", media_types);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves this container's root filesystem. Mounts are not included.
    pub fn rootfs(&self) -> Directory {
        let query = self.selection.select("rootfs");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The error stream of the last executed command.
    /// Returns an error if no command was set.
    pub async fn stderr(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("stderr");
        query.execute(self.graphql_client.clone()).await
    }
    /// The output stream of the last executed command.
    /// Returns an error if no command was set.
    pub async fn stdout(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("stdout");
        query.execute(self.graphql_client.clone()).await
    }
    /// Forces evaluation of the pipeline in the engine.
    /// It doesn't run the default command if no exec has been set.
    pub async fn sync(&self) -> Result<ContainerId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn terminal(&self) -> Container {
        let query = self.selection.select("terminal");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn terminal_opts<'a>(&self, opts: ContainerTerminalOpts<'a>) -> Container {
        let mut query = self.selection.select("terminal");
        if let Some(cmd) = opts.cmd {
            query = query.arg("cmd", cmd);
        }
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.
    /// Be sure to set any exposed ports before calling this api.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn up(&self) -> Result<Void, DaggerError> {
        let query = self.selection.select("up");
        query.execute(self.graphql_client.clone()).await
    }
    /// Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.
    /// Be sure to set any exposed ports before calling this api.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn up_opts<'a>(&self, opts: ContainerUpOpts<'a>) -> Result<Void, DaggerError> {
        let mut query = self.selection.select("up");
        if let Some(ports) = opts.ports {
            query = query.arg("ports", ports);
        }
        if let Some(random) = opts.random {
            query = query.arg("random", random);
        }
        if let Some(args) = opts.args {
            query = query.arg("args", args);
        }
        if let Some(use_entrypoint) = opts.use_entrypoint {
            query = query.arg("useEntrypoint", use_entrypoint);
        }
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        if let Some(no_init) = opts.no_init {
            query = query.arg("noInit", no_init);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the user to be set for all commands.
    pub async fn user(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("user");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves this container plus the given OCI anotation.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the annotation.
    /// * `value` - The value of the annotation.
    pub fn with_annotation(&self, name: impl Into<String>, value: impl Into<String>) -> Container {
        let mut query = self.selection.select("withAnnotation");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Configures default arguments for future commands.
    ///
    /// # Arguments
    ///
    /// * `args` - Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).
    pub fn with_default_args(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withDefaultArgs");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Set the default command to invoke for the container's terminal API.
    ///
    /// # Arguments
    ///
    /// * `args` - The args of the command.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_terminal_cmd(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withDefaultTerminalCmd");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Set the default command to invoke for the container's terminal API.
    ///
    /// # Arguments
    ///
    /// * `args` - The args of the command.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_terminal_cmd_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: ContainerWithDefaultTerminalCmdOpts,
    ) -> Container {
        let mut query = self.selection.select("withDefaultTerminalCmd");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/tmp/directory").
    /// * `directory` - Identifier of the directory to write
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(
        &self,
        path: impl Into<String>,
        directory: impl IntoID<DirectoryId>,
    ) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/tmp/directory").
    /// * `directory` - Identifier of the directory to write
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory_opts<'a>(
        &self,
        path: impl Into<String>,
        directory: impl IntoID<DirectoryId>,
        opts: ContainerWithDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container but with a different command entrypoint.
    ///
    /// # Arguments
    ///
    /// * `args` - Entrypoint to use for future executions (e.g., ["go", "run"]).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_entrypoint(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withEntrypoint");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container but with a different command entrypoint.
    ///
    /// # Arguments
    ///
    /// * `args` - Entrypoint to use for future executions (e.g., ["go", "run"]).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_entrypoint_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: ContainerWithEntrypointOpts,
    ) -> Container {
        let mut query = self.selection.select("withEntrypoint");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        if let Some(keep_default_args) = opts.keep_default_args {
            query = query.arg("keepDefaultArgs", keep_default_args);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the given environment variable.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable (e.g., "HOST").
    /// * `value` - The value of the environment variable. (e.g., "localhost").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_env_variable(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> Container {
        let mut query = self.selection.select("withEnvVariable");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the given environment variable.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable (e.g., "HOST").
    /// * `value` - The value of the environment variable. (e.g., "localhost").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_env_variable_opts(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
        opts: ContainerWithEnvVariableOpts,
    ) -> Container {
        let mut query = self.selection.select("withEnvVariable");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container after executing the specified command inside it.
    ///
    /// # Arguments
    ///
    /// * `args` - Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    ///
    /// If empty, the container's default command is used.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_exec(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withExec");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container after executing the specified command inside it.
    ///
    /// # Arguments
    ///
    /// * `args` - Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
    ///
    /// If empty, the container's default command is used.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_exec_opts<'a>(
        &self,
        args: Vec<impl Into<String>>,
        opts: ContainerWithExecOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withExec");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        if let Some(use_entrypoint) = opts.use_entrypoint {
            query = query.arg("useEntrypoint", use_entrypoint);
        }
        if let Some(stdin) = opts.stdin {
            query = query.arg("stdin", stdin);
        }
        if let Some(redirect_stdout) = opts.redirect_stdout {
            query = query.arg("redirectStdout", redirect_stdout);
        }
        if let Some(redirect_stderr) = opts.redirect_stderr {
            query = query.arg("redirectStderr", redirect_stderr);
        }
        if let Some(expect) = opts.expect {
            query = query.arg("expect", expect);
        }
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        if let Some(no_init) = opts.no_init {
            query = query.arg("noInit", no_init);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Expose a network port.
    /// Exposed ports serve two purposes:
    /// - For health checks and introspection, when running services
    /// - For setting the EXPOSE OCI field when publishing the container
    ///
    /// # Arguments
    ///
    /// * `port` - Port number to expose
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_exposed_port(&self, port: isize) -> Container {
        let mut query = self.selection.select("withExposedPort");
        query = query.arg("port", port);
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Expose a network port.
    /// Exposed ports serve two purposes:
    /// - For health checks and introspection, when running services
    /// - For setting the EXPOSE OCI field when publishing the container
    ///
    /// # Arguments
    ///
    /// * `port` - Port number to expose
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_exposed_port_opts<'a>(
        &self,
        port: isize,
        opts: ContainerWithExposedPortOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withExposedPort");
        query = query.arg("port", port);
        if let Some(protocol) = opts.protocol {
            query = query.arg("protocol", protocol);
        }
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(experimental_skip_healthcheck) = opts.experimental_skip_healthcheck {
            query = query.arg("experimentalSkipHealthcheck", experimental_skip_healthcheck);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: impl IntoID<FileId>) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file_opts<'a>(
        &self,
        path: impl Into<String>,
        source: impl IntoID<FileId>,
        opts: ContainerWithFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the contents of the given files copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location where copied files should be placed (e.g., "/src").
    /// * `sources` - Identifiers of the files to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_files(&self, path: impl Into<String>, sources: Vec<FileId>) -> Container {
        let mut query = self.selection.select("withFiles");
        query = query.arg("path", path.into());
        query = query.arg("sources", sources);
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the contents of the given files copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location where copied files should be placed (e.g., "/src").
    /// * `sources` - Identifiers of the files to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_files_opts<'a>(
        &self,
        path: impl Into<String>,
        sources: Vec<FileId>,
        opts: ContainerWithFilesOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withFiles");
        query = query.arg("path", path.into());
        query = query.arg("sources", sources);
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus the given label.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the label (e.g., "org.opencontainers.artifact.created").
    /// * `value` - The value of the label (e.g., "2023-01-01T00:00:00Z").
    pub fn with_label(&self, name: impl Into<String>, value: impl Into<String>) -> Container {
        let mut query = self.selection.select("withLabel");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/root/.npm").
    /// * `cache` - Identifier of the cache volume to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache(
        &self,
        path: impl Into<String>,
        cache: impl IntoID<CacheVolumeId>,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "cache",
            Box::new(move || {
                let cache = cache.clone();
                Box::pin(async move { cache.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/root/.npm").
    /// * `cache` - Identifier of the cache volume to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache_opts<'a>(
        &self,
        path: impl Into<String>,
        cache: impl IntoID<CacheVolumeId>,
        opts: ContainerWithMountedCacheOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "cache",
            Box::new(move || {
                let cache = cache.clone();
                Box::pin(async move { cache.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(source) = opts.source {
            query = query.arg("source", source);
        }
        if let Some(sharing) = opts.sharing {
            query = query.arg("sharing", sharing);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a directory mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted directory (e.g., "/mnt/directory").
    /// * `source` - Identifier of the mounted directory.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_directory(
        &self,
        path: impl Into<String>,
        source: impl IntoID<DirectoryId>,
    ) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a directory mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted directory (e.g., "/mnt/directory").
    /// * `source` - Identifier of the mounted directory.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_directory_opts<'a>(
        &self,
        path: impl Into<String>,
        source: impl IntoID<DirectoryId>,
        opts: ContainerWithMountedDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a file mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the mounted file.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_file(
        &self,
        path: impl Into<String>,
        source: impl IntoID<FileId>,
    ) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a file mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the mounted file.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_file_opts<'a>(
        &self,
        path: impl Into<String>,
        source: impl IntoID<FileId>,
        opts: ContainerWithMountedFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a secret mounted into a file at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the secret file (e.g., "/tmp/secret.txt").
    /// * `source` - Identifier of the secret to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_secret(
        &self,
        path: impl Into<String>,
        source: impl IntoID<SecretId>,
    ) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a secret mounted into a file at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the secret file (e.g., "/tmp/secret.txt").
    /// * `source` - Identifier of the secret to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_secret_opts<'a>(
        &self,
        path: impl Into<String>,
        source: impl IntoID<SecretId>,
        opts: ContainerWithMountedSecretOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(mode) = opts.mode {
            query = query.arg("mode", mode);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the temporary directory (e.g., "/tmp/temp_dir").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_temp(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withMountedTemp");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the temporary directory (e.g., "/tmp/temp_dir").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_temp_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithMountedTempOpts,
    ) -> Container {
        let mut query = self.selection.select("withMountedTemp");
        query = query.arg("path", path.into());
        if let Some(size) = opts.size {
            query = query.arg("size", size);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/tmp/file.txt").
    /// * `contents` - Content of the file to write (e.g., "Hello world!").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file(&self, path: impl Into<String>, contents: impl Into<String>) -> Container {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        query = query.arg("contents", contents.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/tmp/file.txt").
    /// * `contents` - Content of the file to write (e.g., "Hello world!").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file_opts<'a>(
        &self,
        path: impl Into<String>,
        contents: impl Into<String>,
        opts: ContainerWithNewFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        query = query.arg("contents", contents.into());
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a registry authentication for a given address.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to bind the authentication to.
    ///
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    /// * `username` - The username of the registry's account (e.g., "Dagger").
    /// * `secret` - The API key, password or token to authenticate to this registry.
    pub fn with_registry_auth(
        &self,
        address: impl Into<String>,
        username: impl Into<String>,
        secret: impl IntoID<SecretId>,
    ) -> Container {
        let mut query = self.selection.select("withRegistryAuth");
        query = query.arg("address", address.into());
        query = query.arg("username", username.into());
        query = query.arg_lazy(
            "secret",
            Box::new(move || {
                let secret = secret.clone();
                Box::pin(async move { secret.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves the container with the given directory mounted to /.
    ///
    /// # Arguments
    ///
    /// * `directory` - Directory to mount.
    pub fn with_rootfs(&self, directory: impl IntoID<DirectoryId>) -> Container {
        let mut query = self.selection.select("withRootfs");
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus an env variable containing the given secret.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the secret variable (e.g., "API_SECRET").
    /// * `secret` - The identifier of the secret value.
    pub fn with_secret_variable(
        &self,
        name: impl Into<String>,
        secret: impl IntoID<SecretId>,
    ) -> Container {
        let mut query = self.selection.select("withSecretVariable");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "secret",
            Box::new(move || {
                let secret = secret.clone();
                Box::pin(async move { secret.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Establish a runtime dependency on a service.
    /// The service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.
    /// The service will be reachable from the container via the provided hostname alias.
    /// The service dependency will also convey to any files or directories produced by the container.
    ///
    /// # Arguments
    ///
    /// * `alias` - A name that can be used to reach the service from the container
    /// * `service` - Identifier of the service container
    pub fn with_service_binding(
        &self,
        alias: impl Into<String>,
        service: impl IntoID<ServiceId>,
    ) -> Container {
        let mut query = self.selection.select("withServiceBinding");
        query = query.arg("alias", alias.into());
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a socket forwarded to the given Unix socket path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the forwarded Unix socket (e.g., "/tmp/socket").
    /// * `source` - Identifier of the socket to forward.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_unix_socket(
        &self,
        path: impl Into<String>,
        source: impl IntoID<SocketId>,
    ) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container plus a socket forwarded to the given Unix socket path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the forwarded Unix socket (e.g., "/tmp/socket").
    /// * `source` - Identifier of the socket to forward.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_unix_socket_opts<'a>(
        &self,
        path: impl Into<String>,
        source: impl IntoID<SocketId>,
        opts: ContainerWithUnixSocketOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a different command user.
    ///
    /// # Arguments
    ///
    /// * `name` - The user to set (e.g., "root").
    pub fn with_user(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withUser");
        query = query.arg("name", name.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a different working directory.
    ///
    /// # Arguments
    ///
    /// * `path` - The path to set as the working directory (e.g., "/app").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_workdir(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withWorkdir");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a different working directory.
    ///
    /// # Arguments
    ///
    /// * `path` - The path to set as the working directory (e.g., "/app").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_workdir_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithWorkdirOpts,
    ) -> Container {
        let mut query = self.selection.select("withWorkdir");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container minus the given OCI annotation.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the annotation.
    pub fn without_annotation(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutAnnotation");
        query = query.arg("name", name.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with unset default arguments for future commands.
    pub fn without_default_args(&self) -> Container {
        let query = self.selection.select("withoutDefaultArgs");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the directory at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to remove (e.g., ".github/").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_directory(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutDirectory");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the directory at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to remove (e.g., ".github/").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_directory_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithoutDirectoryOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutDirectory");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with an unset command entrypoint.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_entrypoint(&self) -> Container {
        let query = self.selection.select("withoutEntrypoint");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with an unset command entrypoint.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_entrypoint_opts(&self, opts: ContainerWithoutEntrypointOpts) -> Container {
        let mut query = self.selection.select("withoutEntrypoint");
        if let Some(keep_default_args) = opts.keep_default_args {
            query = query.arg("keepDefaultArgs", keep_default_args);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container minus the given environment variable.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable (e.g., "HOST").
    pub fn without_env_variable(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutEnvVariable");
        query = query.arg("name", name.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Unexpose a previously exposed port.
    ///
    /// # Arguments
    ///
    /// * `port` - Port number to unexpose
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_exposed_port(&self, port: isize) -> Container {
        let mut query = self.selection.select("withoutExposedPort");
        query = query.arg("port", port);
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Unexpose a previously exposed port.
    ///
    /// # Arguments
    ///
    /// * `port` - Port number to unexpose
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_exposed_port_opts(
        &self,
        port: isize,
        opts: ContainerWithoutExposedPortOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutExposedPort");
        query = query.arg("port", port);
        if let Some(protocol) = opts.protocol {
            query = query.arg("protocol", protocol);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the file at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to remove (e.g., "/file.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_file(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutFile");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the file at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to remove (e.g., "/file.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_file_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithoutFileOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutFile");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the files at the given paths removed.
    ///
    /// # Arguments
    ///
    /// * `paths` - Location of the files to remove (e.g., ["/file.txt"]).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_files(&self, paths: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withoutFiles");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with the files at the given paths removed.
    ///
    /// # Arguments
    ///
    /// * `paths` - Location of the files to remove (e.g., ["/file.txt"]).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_files_opts(
        &self,
        paths: Vec<impl Into<String>>,
        opts: ContainerWithoutFilesOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutFiles");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container minus the given environment label.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the label to remove (e.g., "org.opencontainers.artifact.created").
    pub fn without_label(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutLabel");
        query = query.arg("name", name.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container after unmounting everything at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/root/.npm").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_mount(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutMount");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container after unmounting everything at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/root/.npm").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_mount_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithoutMountOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutMount");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container without the registry authentication of a given address.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to remove the authentication from.
    ///
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    pub fn without_registry_auth(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutRegistryAuth");
        query = query.arg("address", address.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container minus the given environment variable containing the secret.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable (e.g., "HOST").
    pub fn without_secret_variable(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutSecretVariable");
        query = query.arg("name", name.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a previously added Unix socket removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the socket to remove (e.g., "/tmp/socket").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_unix_socket(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");
        query = query.arg("path", path.into());
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with a previously added Unix socket removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the socket to remove (e.g., "/tmp/socket").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_unix_socket_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithoutUnixSocketOpts,
    ) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");
        query = query.arg("path", path.into());
        if let Some(expand) = opts.expand {
            query = query.arg("expand", expand);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with an unset command user.
    /// Should default to root.
    pub fn without_user(&self) -> Container {
        let query = self.selection.select("withoutUser");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this container with an unset working directory.
    /// Should default to "/".
    pub fn without_workdir(&self) -> Container {
        let query = self.selection.select("withoutWorkdir");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves the working directory for all commands.
    pub async fn workdir(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("workdir");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct CurrentModule {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct CurrentModuleWorkdirOpts<'a> {
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    #[builder(setter(into, strip_option), default)]
    pub include: Option<Vec<&'a str>>,
}
impl CurrentModule {
    /// A unique identifier for this CurrentModule.
    pub async fn id(&self) -> Result<CurrentModuleId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the module being executed in
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).
    pub fn source(&self) -> Directory {
        let query = self.selection.select("source");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to access (e.g., ".").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn workdir(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("workdir");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to access (e.g., ".").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn workdir_opts<'a>(
        &self,
        path: impl Into<String>,
        opts: CurrentModuleWorkdirOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("workdir");
        query = query.arg("path", path.into());
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to retrieve (e.g., "README.md").
    pub fn workdir_file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("workdirFile");
        query = query.arg("path", path.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Directory {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryAsModuleOpts<'a> {
    /// An optional subpath of the directory which contains the module's configuration file.
    /// If not set, the module source code is loaded from the root of the directory.
    #[builder(setter(into, strip_option), default)]
    pub source_root_path: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryAsModuleSourceOpts<'a> {
    /// An optional subpath of the directory which contains the module's configuration file.
    /// If not set, the module source code is loaded from the root of the directory.
    #[builder(setter(into, strip_option), default)]
    pub source_root_path: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryDockerBuildOpts<'a> {
    /// Build arguments to use in the build.
    #[builder(setter(into, strip_option), default)]
    pub build_args: Option<Vec<BuildArg>>,
    /// Path to the Dockerfile to use (e.g., "frontend.Dockerfile").
    #[builder(setter(into, strip_option), default)]
    pub dockerfile: Option<&'a str>,
    /// If set, skip the automatic init process injected into containers created by RUN statements.
    /// This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
    #[builder(setter(into, strip_option), default)]
    pub no_init: Option<bool>,
    /// The platform to build.
    #[builder(setter(into, strip_option), default)]
    pub platform: Option<Platform>,
    /// Secrets to pass to the build.
    /// They will be mounted at /run/secrets/[secret-name].
    #[builder(setter(into, strip_option), default)]
    pub secret_args: Option<Vec<SecretArg>>,
    /// Target build stage to build.
    #[builder(setter(into, strip_option), default)]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryEntriesOpts<'a> {
    /// Location of the directory to look at (e.g., "/src").
    #[builder(setter(into, strip_option), default)]
    pub path: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryExportOpts {
    /// If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.
    #[builder(setter(into, strip_option), default)]
    pub wipe: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryFilterOpts<'a> {
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    #[builder(setter(into, strip_option), default)]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryTerminalOpts<'a> {
    /// If set, override the container's default terminal command and invoke these command arguments instead.
    #[builder(setter(into, strip_option), default)]
    pub cmd: Option<Vec<&'a str>>,
    /// If set, override the default container used for the terminal.
    #[builder(setter(into, strip_option), default)]
    pub container: Option<ContainerId>,
    /// Provides Dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed; the command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithDirectoryOpts<'a> {
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    #[builder(setter(into, strip_option), default)]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithFileOpts {
    /// Permission given to the copied file (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithFilesOpts {
    /// Permission given to the copied files (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewDirectoryOpts {
    /// Permission granted to the created directory (e.g., 0777).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewFileOpts {
    /// Permission given to the copied file (e.g., 0600).
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
impl Directory {
    /// Converts this directory into a git repository
    pub fn as_git(&self) -> GitRepository {
        let query = self.selection.select("asGit");
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load the directory as a Dagger module source
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module(&self) -> Module {
        let query = self.selection.select("asModule");
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load the directory as a Dagger module source
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module_opts<'a>(&self, opts: DirectoryAsModuleOpts<'a>) -> Module {
        let mut query = self.selection.select("asModule");
        if let Some(source_root_path) = opts.source_root_path {
            query = query.arg("sourceRootPath", source_root_path);
        }
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load the directory as a Dagger module source
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module_source(&self) -> ModuleSource {
        let query = self.selection.select("asModuleSource");
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load the directory as a Dagger module source
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module_source_opts<'a>(&self, opts: DirectoryAsModuleSourceOpts<'a>) -> ModuleSource {
        let mut query = self.selection.select("asModuleSource");
        if let Some(source_root_path) = opts.source_root_path {
            query = query.arg("sourceRootPath", source_root_path);
        }
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Gets the difference between this directory and an another directory.
    ///
    /// # Arguments
    ///
    /// * `other` - Identifier of the directory to compare.
    pub fn diff(&self, other: impl IntoID<DirectoryId>) -> Directory {
        let mut query = self.selection.select("diff");
        query = query.arg_lazy(
            "other",
            Box::new(move || {
                let other = other.clone();
                Box::pin(async move { other.into_id().await.unwrap().quote() })
            }),
        );
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Return the directory's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
    pub async fn digest(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("digest");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves a directory at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to retrieve (e.g., "/src").
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Builds a new Docker container from this directory.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn docker_build(&self) -> Container {
        let query = self.selection.select("dockerBuild");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Builds a new Docker container from this directory.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn docker_build_opts<'a>(&self, opts: DirectoryDockerBuildOpts<'a>) -> Container {
        let mut query = self.selection.select("dockerBuild");
        if let Some(platform) = opts.platform {
            query = query.arg("platform", platform);
        }
        if let Some(dockerfile) = opts.dockerfile {
            query = query.arg("dockerfile", dockerfile);
        }
        if let Some(target) = opts.target {
            query = query.arg("target", target);
        }
        if let Some(build_args) = opts.build_args {
            query = query.arg("buildArgs", build_args);
        }
        if let Some(secret_args) = opts.secret_args {
            query = query.arg("secretArgs", secret_args);
        }
        if let Some(no_init) = opts.no_init {
            query = query.arg("noInit", no_init);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a list of files and directories at the given path.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn entries(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("entries");
        query.execute(self.graphql_client.clone()).await
    }
    /// Returns a list of files and directories at the given path.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn entries_opts<'a>(
        &self,
        opts: DirectoryEntriesOpts<'a>,
    ) -> Result<Vec<String>, DaggerError> {
        let mut query = self.selection.select("entries");
        if let Some(path) = opts.path {
            query = query.arg("path", path);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the contents of the directory to a path on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied directory (e.g., "logs/").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the contents of the directory to a path on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied directory (e.g., "logs/").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: DirectoryExportOpts,
    ) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(wipe) = opts.wipe {
            query = query.arg("wipe", wipe);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves a file at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to retrieve (e.g., "README.md").
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory as per exclude/include filters.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn filter(&self) -> Directory {
        let query = self.selection.select("filter");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory as per exclude/include filters.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn filter_opts<'a>(&self, opts: DirectoryFilterOpts<'a>) -> Directory {
        let mut query = self.selection.select("filter");
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a list of files and directories that matche the given pattern.
    ///
    /// # Arguments
    ///
    /// * `pattern` - Pattern to match (e.g., "*.md").
    pub async fn glob(&self, pattern: impl Into<String>) -> Result<Vec<String>, DaggerError> {
        let mut query = self.selection.select("glob");
        query = query.arg("pattern", pattern.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this Directory.
    pub async fn id(&self) -> Result<DirectoryId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Returns the name of the directory.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// Force evaluation in the engine.
    pub async fn sync(&self) -> Result<DirectoryId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// Opens an interactive terminal in new container with this directory mounted inside.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn terminal(&self) -> Directory {
        let query = self.selection.select("terminal");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Opens an interactive terminal in new container with this directory mounted inside.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn terminal_opts<'a>(&self, opts: DirectoryTerminalOpts<'a>) -> Directory {
        let mut query = self.selection.select("terminal");
        if let Some(cmd) = opts.cmd {
            query = query.arg("cmd", cmd);
        }
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        if let Some(container) = opts.container {
            query = query.arg("container", container);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/src/").
    /// * `directory` - Identifier of the directory to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(
        &self,
        path: impl Into<String>,
        directory: impl IntoID<DirectoryId>,
    ) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.into_id().await.unwrap().quote() })
            }),
        );
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/src/").
    /// * `directory` - Identifier of the directory to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory_opts<'a>(
        &self,
        path: impl Into<String>,
        directory: impl IntoID<DirectoryId>,
        opts: DirectoryWithDirectoryOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: impl IntoID<FileId>) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file_opts(
        &self,
        path: impl Into<String>,
        source: impl IntoID<FileId>,
        opts: DirectoryWithFileOpts,
    ) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus the contents of the given files copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location where copied files should be placed (e.g., "/src").
    /// * `sources` - Identifiers of the files to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_files(&self, path: impl Into<String>, sources: Vec<FileId>) -> Directory {
        let mut query = self.selection.select("withFiles");
        query = query.arg("path", path.into());
        query = query.arg("sources", sources);
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus the contents of the given files copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location where copied files should be placed (e.g., "/src").
    /// * `sources` - Identifiers of the files to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_files_opts(
        &self,
        path: impl Into<String>,
        sources: Vec<FileId>,
        opts: DirectoryWithFilesOpts,
    ) -> Directory {
        let mut query = self.selection.select("withFiles");
        query = query.arg("path", path.into());
        query = query.arg("sources", sources);
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a new directory created at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory created (e.g., "/logs").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withNewDirectory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a new directory created at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory created (e.g., "/logs").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_directory_opts(
        &self,
        path: impl Into<String>,
        opts: DirectoryWithNewDirectoryOpts,
    ) -> Directory {
        let mut query = self.selection.select("withNewDirectory");
        query = query.arg("path", path.into());
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/file.txt").
    /// * `contents` - Content of the written file (e.g., "Hello world!").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file(&self, path: impl Into<String>, contents: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        query = query.arg("contents", contents.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/file.txt").
    /// * `contents` - Content of the written file (e.g., "Hello world!").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file_opts(
        &self,
        path: impl Into<String>,
        contents: impl Into<String>,
        opts: DirectoryWithNewFileOpts,
    ) -> Directory {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        query = query.arg("contents", contents.into());
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory with all file/dir timestamps set to the given time.
    ///
    /// # Arguments
    ///
    /// * `timestamp` - Timestamp to set dir/files in.
    ///
    /// Formatted in seconds following Unix epoch (e.g., 1672531199).
    pub fn with_timestamps(&self, timestamp: isize) -> Directory {
        let mut query = self.selection.select("withTimestamps");
        query = query.arg("timestamp", timestamp);
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory with the directory at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to remove (e.g., ".github/").
    pub fn without_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutDirectory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory with the file at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to remove (e.g., "/file.txt").
    pub fn without_file(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutFile");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this directory with the files at the given paths removed.
    ///
    /// # Arguments
    ///
    /// * `paths` - Location of the file to remove (e.g., ["/file.txt"]).
    pub fn without_files(&self, paths: Vec<impl Into<String>>) -> Directory {
        let mut query = self.selection.select("withoutFiles");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Engine {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Engine {
    /// A unique identifier for this Engine.
    pub async fn id(&self) -> Result<EngineId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The local (on-disk) cache for the Dagger engine
    pub fn local_cache(&self) -> EngineCache {
        let query = self.selection.select("localCache");
        EngineCache {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct EngineCache {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct EngineCacheEntrySetOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub key: Option<&'a str>,
}
impl EngineCache {
    /// The current set of entries in the cache
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn entry_set(&self) -> EngineCacheEntrySet {
        let query = self.selection.select("entrySet");
        EngineCacheEntrySet {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The current set of entries in the cache
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn entry_set_opts<'a>(&self, opts: EngineCacheEntrySetOpts<'a>) -> EngineCacheEntrySet {
        let mut query = self.selection.select("entrySet");
        if let Some(key) = opts.key {
            query = query.arg("key", key);
        }
        EngineCacheEntrySet {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this EngineCache.
    pub async fn id(&self) -> Result<EngineCacheId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The maximum bytes to keep in the cache without pruning, after which automatic pruning may kick in.
    pub async fn keep_bytes(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("keepBytes");
        query.execute(self.graphql_client.clone()).await
    }
    /// The maximum bytes to keep in the cache without pruning.
    pub async fn max_used_space(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("maxUsedSpace");
        query.execute(self.graphql_client.clone()).await
    }
    /// The target amount of free disk space the garbage collector will attempt to leave.
    pub async fn min_free_space(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("minFreeSpace");
        query.execute(self.graphql_client.clone()).await
    }
    /// Prune the cache of releaseable entries
    pub async fn prune(&self) -> Result<Void, DaggerError> {
        let query = self.selection.select("prune");
        query.execute(self.graphql_client.clone()).await
    }
    pub async fn reserved_space(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("reservedSpace");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct EngineCacheEntry {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EngineCacheEntry {
    /// Whether the cache entry is actively being used.
    pub async fn actively_used(&self) -> Result<bool, DaggerError> {
        let query = self.selection.select("activelyUsed");
        query.execute(self.graphql_client.clone()).await
    }
    /// The time the cache entry was created, in Unix nanoseconds.
    pub async fn created_time_unix_nano(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("createdTimeUnixNano");
        query.execute(self.graphql_client.clone()).await
    }
    /// The description of the cache entry.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The disk space used by the cache entry.
    pub async fn disk_space_bytes(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("diskSpaceBytes");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this EngineCacheEntry.
    pub async fn id(&self) -> Result<EngineCacheEntryId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The most recent time the cache entry was used, in Unix nanoseconds.
    pub async fn most_recent_use_time_unix_nano(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("mostRecentUseTimeUnixNano");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct EngineCacheEntrySet {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EngineCacheEntrySet {
    /// The total disk space used by the cache entries in this set.
    pub async fn disk_space_bytes(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("diskSpaceBytes");
        query.execute(self.graphql_client.clone()).await
    }
    /// The list of individual cache entries in the set
    pub fn entries(&self) -> Vec<EngineCacheEntry> {
        let query = self.selection.select("entries");
        vec![EngineCacheEntry {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The number of cache entries in this set.
    pub async fn entry_count(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("entryCount");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this EngineCacheEntrySet.
    pub async fn id(&self) -> Result<EngineCacheEntrySetId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct EnumTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EnumTypeDef {
    /// A doc string for the enum, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this EnumTypeDef.
    pub async fn id(&self) -> Result<EnumTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the enum.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this enum declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise.
    pub async fn source_module_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(self.graphql_client.clone()).await
    }
    /// The values of the enum.
    pub fn values(&self) -> Vec<EnumValueTypeDef> {
        let query = self.selection.select("values");
        vec![EnumValueTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
}
#[derive(Clone)]
pub struct EnumValueTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EnumValueTypeDef {
    /// A doc string for the enum value, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this EnumValueTypeDef.
    pub async fn id(&self) -> Result<EnumValueTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the enum value.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this enum value declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Env {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Env {
    /// A unique identifier for this Env.
    pub async fn id(&self) -> Result<EnvId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// retrieve an input value by name
    pub fn input(&self, name: impl Into<String>) -> Binding {
        let mut query = self.selection.select("input");
        query = query.arg("name", name.into());
        Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// return all input values for the environment
    pub fn inputs(&self) -> Vec<Binding> {
        let query = self.selection.select("inputs");
        vec![Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// retrieve an output value by name
    pub fn output(&self, name: impl Into<String>) -> Binding {
        let mut query = self.selection.select("output");
        query = query.arg("name", name.into());
        Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// return all output values for the environment
    pub fn outputs(&self) -> Vec<Binding> {
        let query = self.selection.select("outputs");
        vec![Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Create or update a binding of type CacheVolume in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The CacheVolume value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_cache_volume_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<CacheVolumeId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withCacheVolumeInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired CacheVolume output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_cache_volume_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withCacheVolumeOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Container in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Container value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_container_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<ContainerId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withContainerInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Container output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_container_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withContainerOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Directory in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Directory value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_directory_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<DirectoryId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withDirectoryInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Directory output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_directory_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withDirectoryOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Env in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Env value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_env_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<EnvId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withEnvInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Env output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_env_output(&self, name: impl Into<String>, description: impl Into<String>) -> Env {
        let mut query = self.selection.select("withEnvOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type File in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The File value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_file_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<FileId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withFileInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired File output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_file_output(&self, name: impl Into<String>, description: impl Into<String>) -> Env {
        let mut query = self.selection.select("withFileOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type GitRef in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The GitRef value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_git_ref_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<GitRefId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withGitRefInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired GitRef output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_git_ref_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withGitRefOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type GitRepository in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The GitRepository value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_git_repository_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<GitRepositoryId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withGitRepositoryInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired GitRepository output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_git_repository_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withGitRepositoryOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type LLM in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The LLM value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_llm_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<Llmid>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withLLMInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired LLM output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_llm_output(&self, name: impl Into<String>, description: impl Into<String>) -> Env {
        let mut query = self.selection.select("withLLMOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type ModuleConfigClient in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The ModuleConfigClient value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_module_config_client_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<ModuleConfigClientId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleConfigClientInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired ModuleConfigClient output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_module_config_client_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleConfigClientOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Module in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Module value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_module_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<ModuleId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Module output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_module_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type ModuleSource in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The ModuleSource value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_module_source_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<ModuleSourceId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleSourceInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired ModuleSource output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_module_source_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withModuleSourceOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Secret in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Secret value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_secret_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<SecretId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withSecretInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Secret output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_secret_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withSecretOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Service in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Service value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_service_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<ServiceId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withServiceInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Service output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_service_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withServiceOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update a binding of type Socket in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The Socket value to assign to the binding
    /// * `description` - The purpose of the input
    pub fn with_socket_input(
        &self,
        name: impl Into<String>,
        value: impl IntoID<SocketId>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withSocketInput");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "value",
            Box::new(move || {
                let value = value.clone();
                Box::pin(async move { value.into_id().await.unwrap().quote() })
            }),
        );
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Declare a desired Socket output to be assigned in the environment
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `description` - A description of the desired value of the binding
    pub fn with_socket_output(
        &self,
        name: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withSocketOutput");
        query = query.arg("name", name.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create or update an input value of type string
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the binding
    /// * `value` - The string value to assign to the binding
    pub fn with_string_input(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
        description: impl Into<String>,
    ) -> Env {
        let mut query = self.selection.select("withStringInput");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        query = query.arg("description", description.into());
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct EnvVariable {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EnvVariable {
    /// A unique identifier for this EnvVariable.
    pub async fn id(&self) -> Result<EnvVariableId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The environment variable name.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The environment variable value.
    pub async fn value(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("value");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Error {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Error {
    /// A unique identifier for this Error.
    pub async fn id(&self) -> Result<ErrorId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// A description of the error.
    pub async fn message(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("message");
        query.execute(self.graphql_client.clone()).await
    }
    /// The extensions of the error.
    pub fn values(&self) -> Vec<ErrorValue> {
        let query = self.selection.select("values");
        vec![ErrorValue {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Add a value to the error.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the value.
    /// * `value` - The value to store on the error.
    pub fn with_value(&self, name: impl Into<String>, value: Json) -> Error {
        let mut query = self.selection.select("withValue");
        query = query.arg("name", name.into());
        query = query.arg("value", value);
        Error {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct ErrorValue {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ErrorValue {
    /// A unique identifier for this ErrorValue.
    pub async fn id(&self) -> Result<ErrorValueId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the value.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value.
    pub async fn value(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("value");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct FieldTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FieldTypeDef {
    /// A doc string for the field, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this FieldTypeDef.
    pub async fn id(&self) -> Result<FieldTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the field in lowerCamelCase format.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this field declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The type of the field.
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct File {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct FileDigestOpts {
    /// If true, exclude metadata from the digest.
    #[builder(setter(into, strip_option), default)]
    pub exclude_metadata: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct FileExportOpts {
    /// If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.
    #[builder(setter(into, strip_option), default)]
    pub allow_parent_dir_path: Option<bool>,
}
impl File {
    /// Retrieves the contents of the file.
    pub async fn contents(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("contents");
        query.execute(self.graphql_client.clone()).await
    }
    /// Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn digest(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("digest");
        query.execute(self.graphql_client.clone()).await
    }
    /// Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn digest_opts(&self, opts: FileDigestOpts) -> Result<String, DaggerError> {
        let mut query = self.selection.select("digest");
        if let Some(exclude_metadata) = opts.exclude_metadata {
            query = query.arg("excludeMetadata", exclude_metadata);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the file to a file path on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "output.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the file to a file path on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "output.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: FileExportOpts,
    ) -> Result<String, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(allow_parent_dir_path) = opts.allow_parent_dir_path {
            query = query.arg("allowParentDirPath", allow_parent_dir_path);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this File.
    pub async fn id(&self) -> Result<FileId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the name of the file.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the size of the file, in bytes.
    pub async fn size(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("size");
        query.execute(self.graphql_client.clone()).await
    }
    /// Force evaluation in the engine.
    pub async fn sync(&self) -> Result<FileId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves this file with its name set to the given name.
    ///
    /// # Arguments
    ///
    /// * `name` - Name to set file to.
    pub fn with_name(&self, name: impl Into<String>) -> File {
        let mut query = self.selection.select("withName");
        query = query.arg("name", name.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Retrieves this file with its created/modified timestamps set to the given time.
    ///
    /// # Arguments
    ///
    /// * `timestamp` - Timestamp to set dir/files in.
    ///
    /// Formatted in seconds following Unix epoch (e.g., 1672531199).
    pub fn with_timestamps(&self, timestamp: isize) -> File {
        let mut query = self.selection.select("withTimestamps");
        query = query.arg("timestamp", timestamp);
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Function {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct FunctionWithArgOpts<'a> {
    /// If the argument is a Directory or File type, default to load path from context directory, relative to root directory.
    #[builder(setter(into, strip_option), default)]
    pub default_path: Option<&'a str>,
    /// A default value to use for this argument if not explicitly set by the caller, if any
    #[builder(setter(into, strip_option), default)]
    pub default_value: Option<Json>,
    /// A doc string for the argument, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Patterns to ignore when loading the contextual argument value.
    #[builder(setter(into, strip_option), default)]
    pub ignore: Option<Vec<&'a str>>,
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
impl Function {
    /// Arguments accepted by the function, if any.
    pub fn args(&self) -> Vec<FunctionArg> {
        let query = self.selection.select("args");
        vec![FunctionArg {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// A doc string for the function, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this Function.
    pub async fn id(&self) -> Result<FunctionId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the function.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The type returned by the function.
    pub fn return_type(&self) -> TypeDef {
        let query = self.selection.select("returnType");
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The location of this function declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns the function with the provided argument
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the argument
    /// * `type_def` - The type of the argument
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_arg(&self, name: impl Into<String>, type_def: impl IntoID<TypeDefId>) -> Function {
        let mut query = self.selection.select("withArg");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.into_id().await.unwrap().quote() })
            }),
        );
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns the function with the provided argument
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the argument
    /// * `type_def` - The type of the argument
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_arg_opts<'a>(
        &self,
        name: impl Into<String>,
        type_def: impl IntoID<TypeDefId>,
        opts: FunctionWithArgOpts<'a>,
    ) -> Function {
        let mut query = self.selection.select("withArg");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(default_value) = opts.default_value {
            query = query.arg("defaultValue", default_value);
        }
        if let Some(default_path) = opts.default_path {
            query = query.arg("defaultPath", default_path);
        }
        if let Some(ignore) = opts.ignore {
            query = query.arg("ignore", ignore);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns the function with the given doc string.
    ///
    /// # Arguments
    ///
    /// * `description` - The doc string to set.
    pub fn with_description(&self, description: impl Into<String>) -> Function {
        let mut query = self.selection.select("withDescription");
        query = query.arg("description", description.into());
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns the function with the given source map.
    ///
    /// # Arguments
    ///
    /// * `source_map` - The source map for the function definition.
    pub fn with_source_map(&self, source_map: impl IntoID<SourceMapId>) -> Function {
        let mut query = self.selection.select("withSourceMap");
        query = query.arg_lazy(
            "sourceMap",
            Box::new(move || {
                let source_map = source_map.clone();
                Box::pin(async move { source_map.into_id().await.unwrap().quote() })
            }),
        );
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct FunctionArg {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionArg {
    /// Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory
    pub async fn default_path(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("defaultPath");
        query.execute(self.graphql_client.clone()).await
    }
    /// A default value to use for this argument when not explicitly set by the caller, if any.
    pub async fn default_value(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("defaultValue");
        query.execute(self.graphql_client.clone()).await
    }
    /// A doc string for the argument, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this FunctionArg.
    pub async fn id(&self) -> Result<FunctionArgId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner.
    pub async fn ignore(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("ignore");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the argument in lowerCamelCase format.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this arg declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The type of the argument.
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct FunctionCall {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionCall {
    /// A unique identifier for this FunctionCall.
    pub async fn id(&self) -> Result<FunctionCallId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The argument values the function is being invoked with.
    pub fn input_args(&self) -> Vec<FunctionCallArgValue> {
        let query = self.selection.select("inputArgs");
        vec![FunctionCallArgValue {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The name of the function being called.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.
    pub async fn parent(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("parent");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.
    pub async fn parent_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("parentName");
        query.execute(self.graphql_client.clone()).await
    }
    /// Return an error from the function.
    ///
    /// # Arguments
    ///
    /// * `error` - The error to return.
    pub async fn return_error(&self, error: impl IntoID<ErrorId>) -> Result<Void, DaggerError> {
        let mut query = self.selection.select("returnError");
        query = query.arg_lazy(
            "error",
            Box::new(move || {
                let error = error.clone();
                Box::pin(async move { error.into_id().await.unwrap().quote() })
            }),
        );
        query.execute(self.graphql_client.clone()).await
    }
    /// Set the return value of the function call to the provided value.
    ///
    /// # Arguments
    ///
    /// * `value` - JSON serialization of the return value.
    pub async fn return_value(&self, value: Json) -> Result<Void, DaggerError> {
        let mut query = self.selection.select("returnValue");
        query = query.arg("value", value);
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct FunctionCallArgValue {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionCallArgValue {
    /// A unique identifier for this FunctionCallArgValue.
    pub async fn id(&self) -> Result<FunctionCallArgValueId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the argument.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of the argument represented as a JSON serialized string.
    pub async fn value(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("value");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct GeneratedCode {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl GeneratedCode {
    /// The directory containing the generated code.
    pub fn code(&self) -> Directory {
        let query = self.selection.select("code");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this GeneratedCode.
    pub async fn id(&self) -> Result<GeneratedCodeId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// List of paths to mark generated in version control (i.e. .gitattributes).
    pub async fn vcs_generated_paths(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("vcsGeneratedPaths");
        query.execute(self.graphql_client.clone()).await
    }
    /// List of paths to ignore in version control (i.e. .gitignore).
    pub async fn vcs_ignored_paths(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("vcsIgnoredPaths");
        query.execute(self.graphql_client.clone()).await
    }
    /// Set the list of paths to mark generated in version control.
    pub fn with_vcs_generated_paths(&self, paths: Vec<impl Into<String>>) -> GeneratedCode {
        let mut query = self.selection.select("withVCSGeneratedPaths");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Set the list of paths to ignore in version control.
    pub fn with_vcs_ignored_paths(&self, paths: Vec<impl Into<String>>) -> GeneratedCode {
        let mut query = self.selection.select("withVCSIgnoredPaths");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct GitRef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct GitRefTreeOpts {
    /// Set to true to discard .git directory.
    #[builder(setter(into, strip_option), default)]
    pub discard_git_dir: Option<bool>,
}
impl GitRef {
    /// The resolved commit id at this ref.
    pub async fn commit(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("commit");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this GitRef.
    pub async fn id(&self) -> Result<GitRefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The filesystem tree at this ref.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tree(&self) -> Directory {
        let query = self.selection.select("tree");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The filesystem tree at this ref.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tree_opts(&self, opts: GitRefTreeOpts) -> Directory {
        let mut query = self.selection.select("tree");
        if let Some(discard_git_dir) = opts.discard_git_dir {
            query = query.arg("discardGitDir", discard_git_dir);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct GitRepository {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct GitRepositoryTagsOpts<'a> {
    /// Glob patterns (e.g., "refs/tags/v*").
    #[builder(setter(into, strip_option), default)]
    pub patterns: Option<Vec<&'a str>>,
}
impl GitRepository {
    /// Returns details of a branch.
    ///
    /// # Arguments
    ///
    /// * `name` - Branch's name (e.g., "main").
    pub fn branch(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("branch");
        query = query.arg("name", name.into());
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns details of a commit.
    ///
    /// # Arguments
    ///
    /// * `id` - Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").
    pub fn commit(&self, id: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("commit");
        query = query.arg("id", id.into());
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns details for HEAD.
    pub fn head(&self) -> GitRef {
        let query = self.selection.select("head");
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this GitRepository.
    pub async fn id(&self) -> Result<GitRepositoryId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Returns details of a ref.
    ///
    /// # Arguments
    ///
    /// * `name` - Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).
    pub fn r#ref(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("ref");
        query = query.arg("name", name.into());
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns details of a tag.
    ///
    /// # Arguments
    ///
    /// * `name` - Tag's name (e.g., "v0.3.9").
    pub fn tag(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("tag");
        query = query.arg("name", name.into());
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// tags that match any of the given glob patterns.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn tags(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("tags");
        query.execute(self.graphql_client.clone()).await
    }
    /// tags that match any of the given glob patterns.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn tags_opts<'a>(
        &self,
        opts: GitRepositoryTagsOpts<'a>,
    ) -> Result<Vec<String>, DaggerError> {
        let mut query = self.selection.select("tags");
        if let Some(patterns) = opts.patterns {
            query = query.arg("patterns", patterns);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Header to authenticate the remote with.
    ///
    /// # Arguments
    ///
    /// * `header` - Secret used to populate the Authorization HTTP header
    pub fn with_auth_header(&self, header: impl IntoID<SecretId>) -> GitRepository {
        let mut query = self.selection.select("withAuthHeader");
        query = query.arg_lazy(
            "header",
            Box::new(move || {
                let header = header.clone();
                Box::pin(async move { header.into_id().await.unwrap().quote() })
            }),
        );
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Token to authenticate the remote with.
    ///
    /// # Arguments
    ///
    /// * `token` - Secret used to populate the password during basic HTTP Authorization
    pub fn with_auth_token(&self, token: impl IntoID<SecretId>) -> GitRepository {
        let mut query = self.selection.select("withAuthToken");
        query = query.arg_lazy(
            "token",
            Box::new(move || {
                let token = token.clone();
                Box::pin(async move { token.into_id().await.unwrap().quote() })
            }),
        );
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Host {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct HostDirectoryOpts<'a> {
    /// Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
    #[builder(setter(into, strip_option), default)]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct HostServiceOpts<'a> {
    /// Upstream host to forward traffic to.
    #[builder(setter(into, strip_option), default)]
    pub host: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct HostTunnelOpts {
    /// Map each service port to the same port on the host, as if the service were running natively.
    /// Note: enabling may result in port conflicts.
    #[builder(setter(into, strip_option), default)]
    pub native: Option<bool>,
    /// Configure explicit port forwarding rules for the tunnel.
    /// If a port's frontend is unspecified or 0, a random port will be chosen by the host.
    /// If no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host.
    /// If ports are given and native is true, the ports are additive.
    #[builder(setter(into, strip_option), default)]
    pub ports: Option<Vec<PortForward>>,
}
impl Host {
    /// Accesses a directory on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to access (e.g., ".").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Accesses a directory on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to access (e.g., ".").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory_opts<'a>(
        &self,
        path: impl Into<String>,
        opts: HostDirectoryOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Accesses a file on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to retrieve (e.g., "README.md").
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this Host.
    pub async fn id(&self) -> Result<HostId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a service that forwards traffic to a specified address via the host.
    ///
    /// # Arguments
    ///
    /// * `ports` - Ports to expose via the service, forwarding through the host network.
    ///
    /// If a port's frontend is unspecified or 0, it defaults to the same as the backend port.
    ///
    /// An empty set of ports is not valid; an error will be returned.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn service(&self, ports: Vec<PortForward>) -> Service {
        let mut query = self.selection.select("service");
        query = query.arg("ports", ports);
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a service that forwards traffic to a specified address via the host.
    ///
    /// # Arguments
    ///
    /// * `ports` - Ports to expose via the service, forwarding through the host network.
    ///
    /// If a port's frontend is unspecified or 0, it defaults to the same as the backend port.
    ///
    /// An empty set of ports is not valid; an error will be returned.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn service_opts<'a>(&self, ports: Vec<PortForward>, opts: HostServiceOpts<'a>) -> Service {
        let mut query = self.selection.select("service");
        query = query.arg("ports", ports);
        if let Some(host) = opts.host {
            query = query.arg("host", host);
        }
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Sets a secret given a user-defined name and the file path on the host, and returns the secret.
    /// The file is limited to a size of 512000 bytes.
    ///
    /// # Arguments
    ///
    /// * `name` - The user defined name for this secret.
    /// * `path` - Location of the file to set as a secret.
    pub fn set_secret_file(&self, name: impl Into<String>, path: impl Into<String>) -> Secret {
        let mut query = self.selection.select("setSecretFile");
        query = query.arg("name", name.into());
        query = query.arg("path", path.into());
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a tunnel that forwards traffic from the host to a service.
    ///
    /// # Arguments
    ///
    /// * `service` - Service to send traffic from the tunnel.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tunnel(&self, service: impl IntoID<ServiceId>) -> Service {
        let mut query = self.selection.select("tunnel");
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.into_id().await.unwrap().quote() })
            }),
        );
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a tunnel that forwards traffic from the host to a service.
    ///
    /// # Arguments
    ///
    /// * `service` - Service to send traffic from the tunnel.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tunnel_opts(&self, service: impl IntoID<ServiceId>, opts: HostTunnelOpts) -> Service {
        let mut query = self.selection.select("tunnel");
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(ports) = opts.ports {
            query = query.arg("ports", ports);
        }
        if let Some(native) = opts.native {
            query = query.arg("native", native);
        }
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Accesses a Unix socket on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the Unix socket (e.g., "/var/run/docker.sock").
    pub fn unix_socket(&self, path: impl Into<String>) -> Socket {
        let mut query = self.selection.select("unixSocket");
        query = query.arg("path", path.into());
        Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct InputTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl InputTypeDef {
    /// Static fields defined on this input object, if any.
    pub fn fields(&self) -> Vec<FieldTypeDef> {
        let query = self.selection.select("fields");
        vec![FieldTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// A unique identifier for this InputTypeDef.
    pub async fn id(&self) -> Result<InputTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the input object.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct InterfaceTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl InterfaceTypeDef {
    /// The doc string for the interface, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Functions defined on this interface, if any.
    pub fn functions(&self) -> Vec<Function> {
        let query = self.selection.select("functions");
        vec![Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// A unique identifier for this InterfaceTypeDef.
    pub async fn id(&self) -> Result<InterfaceTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the interface.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this interface declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.
    pub async fn source_module_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Llm {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Llm {
    /// create a branch in the LLM's history
    pub fn attempt(&self, number: isize) -> Llm {
        let mut query = self.selection.select("attempt");
        query = query.arg("number", number);
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// returns the type of the current state
    pub fn bind_result(&self, name: impl Into<String>) -> Binding {
        let mut query = self.selection.select("bindResult");
        query = query.arg("name", name.into());
        Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// return the LLM's current environment
    pub fn env(&self) -> Env {
        let query = self.selection.select("env");
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// return the llm message history
    pub async fn history(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("history");
        query.execute(self.graphql_client.clone()).await
    }
    /// return the raw llm message history as json
    pub async fn history_json(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("historyJSON");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this LLM.
    pub async fn id(&self) -> Result<Llmid, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// return the last llm reply from the history
    pub async fn last_reply(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("lastReply");
        query.execute(self.graphql_client.clone()).await
    }
    /// synchronize LLM state
    pub fn r#loop(&self) -> Llm {
        let query = self.selection.select("loop");
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// return the model used by the llm
    pub async fn model(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("model");
        query.execute(self.graphql_client.clone()).await
    }
    /// return the provider used by the llm
    pub async fn provider(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("provider");
        query.execute(self.graphql_client.clone()).await
    }
    /// synchronize LLM state
    pub async fn sync(&self) -> Result<Llmid, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// returns the token usage of the current state
    pub fn token_usage(&self) -> LlmTokenUsage {
        let query = self.selection.select("tokenUsage");
        LlmTokenUsage {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// print documentation for available tools
    pub async fn tools(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("tools");
        query.execute(self.graphql_client.clone()).await
    }
    /// allow the LLM to interact with an environment via MCP
    pub fn with_env(&self, env: impl IntoID<EnvId>) -> Llm {
        let mut query = self.selection.select("withEnv");
        query = query.arg_lazy(
            "env",
            Box::new(move || {
                let env = env.clone();
                Box::pin(async move { env.into_id().await.unwrap().quote() })
            }),
        );
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// swap out the llm model
    ///
    /// # Arguments
    ///
    /// * `model` - The model to use
    pub fn with_model(&self, model: impl Into<String>) -> Llm {
        let mut query = self.selection.select("withModel");
        query = query.arg("model", model.into());
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// append a prompt to the llm context
    ///
    /// # Arguments
    ///
    /// * `prompt` - The prompt to send
    pub fn with_prompt(&self, prompt: impl Into<String>) -> Llm {
        let mut query = self.selection.select("withPrompt");
        query = query.arg("prompt", prompt.into());
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// append the contents of a file to the llm context
    ///
    /// # Arguments
    ///
    /// * `file` - The file to read the prompt from
    pub fn with_prompt_file(&self, file: impl IntoID<FileId>) -> Llm {
        let mut query = self.selection.select("withPromptFile");
        query = query.arg_lazy(
            "file",
            Box::new(move || {
                let file = file.clone();
                Box::pin(async move { file.into_id().await.unwrap().quote() })
            }),
        );
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Add a system prompt to the LLM's environment
    ///
    /// # Arguments
    ///
    /// * `prompt` - The system prompt to send
    pub fn with_system_prompt(&self, prompt: impl Into<String>) -> Llm {
        let mut query = self.selection.select("withSystemPrompt");
        query = query.arg("prompt", prompt.into());
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct LlmTokenUsage {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl LlmTokenUsage {
    /// A unique identifier for this LLMTokenUsage.
    pub async fn id(&self) -> Result<LlmTokenUsageId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    pub async fn input_tokens(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("inputTokens");
        query.execute(self.graphql_client.clone()).await
    }
    pub async fn output_tokens(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("outputTokens");
        query.execute(self.graphql_client.clone()).await
    }
    pub async fn total_tokens(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("totalTokens");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Label {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Label {
    /// A unique identifier for this Label.
    pub async fn id(&self) -> Result<LabelId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The label name.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The label value.
    pub async fn value(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("value");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct ListTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ListTypeDef {
    /// The type of the elements in the list.
    pub fn element_type_def(&self) -> TypeDef {
        let query = self.selection.select("elementTypeDef");
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this ListTypeDef.
    pub async fn id(&self) -> Result<ListTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Module {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Module {
    /// The dependencies of the module.
    pub fn dependencies(&self) -> Vec<Module> {
        let query = self.selection.select("dependencies");
        vec![Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The doc string of the module, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Enumerations served by this module.
    pub fn enums(&self) -> Vec<TypeDef> {
        let query = self.selection.select("enums");
        vec![TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The generated files and directories made on top of the module source's context directory.
    pub fn generated_context_directory(&self) -> Directory {
        let query = self.selection.select("generatedContextDirectory");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this Module.
    pub async fn id(&self) -> Result<ModuleId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Interfaces served by this module.
    pub fn interfaces(&self) -> Vec<TypeDef> {
        let query = self.selection.select("interfaces");
        vec![TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The name of the module
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// Objects served by this module.
    pub fn objects(&self) -> Vec<TypeDef> {
        let query = self.selection.select("objects");
        vec![TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
    pub fn runtime(&self) -> Container {
        let query = self.selection.select("runtime");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The SDK config used by this module.
    pub fn sdk(&self) -> SdkConfig {
        let query = self.selection.select("sdk");
        SdkConfig {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Serve a module's API in the current session.
    /// Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.
    pub async fn serve(&self) -> Result<Void, DaggerError> {
        let query = self.selection.select("serve");
        query.execute(self.graphql_client.clone()).await
    }
    /// The source for the module.
    pub fn source(&self) -> ModuleSource {
        let query = self.selection.select("source");
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Forces evaluation of the module, including any loading into the engine and associated validation.
    pub async fn sync(&self) -> Result<ModuleId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the module with the given description
    ///
    /// # Arguments
    ///
    /// * `description` - The description to set
    pub fn with_description(&self, description: impl Into<String>) -> Module {
        let mut query = self.selection.select("withDescription");
        query = query.arg("description", description.into());
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// This module plus the given Enum type and associated values
    pub fn with_enum(&self, r#enum: impl IntoID<TypeDefId>) -> Module {
        let mut query = self.selection.select("withEnum");
        query = query.arg_lazy(
            "enum",
            Box::new(move || {
                let r#enum = r#enum.clone();
                Box::pin(async move { r#enum.into_id().await.unwrap().quote() })
            }),
        );
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// This module plus the given Interface type and associated functions
    pub fn with_interface(&self, iface: impl IntoID<TypeDefId>) -> Module {
        let mut query = self.selection.select("withInterface");
        query = query.arg_lazy(
            "iface",
            Box::new(move || {
                let iface = iface.clone();
                Box::pin(async move { iface.into_id().await.unwrap().quote() })
            }),
        );
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// This module plus the given Object type and associated functions.
    pub fn with_object(&self, object: impl IntoID<TypeDefId>) -> Module {
        let mut query = self.selection.select("withObject");
        query = query.arg_lazy(
            "object",
            Box::new(move || {
                let object = object.clone();
                Box::pin(async move { object.into_id().await.unwrap().quote() })
            }),
        );
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct ModuleConfigClient {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ModuleConfigClient {
    /// If true, generate the client in developer mode.
    pub async fn dev(&self) -> Result<bool, DaggerError> {
        let query = self.selection.select("dev");
        query.execute(self.graphql_client.clone()).await
    }
    /// The directory the client is generated in.
    pub async fn directory(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("directory");
        query.execute(self.graphql_client.clone()).await
    }
    /// The generator to use
    pub async fn generator(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("generator");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this ModuleConfigClient.
    pub async fn id(&self) -> Result<ModuleConfigClientId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct ModuleSource {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ModuleSourceWithClientOpts {
    /// Generate in developer mode
    #[builder(setter(into, strip_option), default)]
    pub dev: Option<bool>,
}
impl ModuleSource {
    /// Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation
    pub fn as_module(&self) -> Module {
        let query = self.selection.select("asModule");
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A human readable ref string representation of this module source.
    pub async fn as_string(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("asString");
        query.execute(self.graphql_client.clone()).await
    }
    /// The ref to clone the root of the git repo from. Only valid for git sources.
    pub async fn clone_ref(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("cloneRef");
        query.execute(self.graphql_client.clone()).await
    }
    /// The resolved commit of the git repo this source points to.
    pub async fn commit(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("commit");
        query.execute(self.graphql_client.clone()).await
    }
    /// The clients generated for the module.
    pub fn config_clients(&self) -> Vec<ModuleConfigClient> {
        let query = self.selection.select("configClients");
        vec![ModuleConfigClient {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Whether an existing dagger.json for the module was found.
    pub async fn config_exists(&self) -> Result<bool, DaggerError> {
        let query = self.selection.select("configExists");
        query.execute(self.graphql_client.clone()).await
    }
    /// The full directory loaded for the module source, including the source code as a subdirectory.
    pub fn context_directory(&self) -> Directory {
        let query = self.selection.select("contextDirectory");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The dependencies of the module source.
    pub fn dependencies(&self) -> Vec<ModuleSource> {
        let query = self.selection.select("dependencies");
        vec![ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance.
    pub async fn digest(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("digest");
        query.execute(self.graphql_client.clone()).await
    }
    /// The directory containing the module configuration and source code (source code may be in a subdir).
    ///
    /// # Arguments
    ///
    /// * `path` - A subpath from the source directory to select.
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The engine version of the module.
    pub async fn engine_version(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("engineVersion");
        query.execute(self.graphql_client.clone()).await
    }
    /// The generated files and directories made on top of the module source's context directory.
    pub fn generated_context_directory(&self) -> Directory {
        let query = self.selection.select("generatedContextDirectory");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).
    pub async fn html_repo_url(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("htmlRepoURL");
        query.execute(self.graphql_client.clone()).await
    }
    /// The URL to the source's git repo in a web browser. Only valid for git sources.
    pub async fn html_url(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("htmlURL");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this ModuleSource.
    pub async fn id(&self) -> Result<ModuleSourceId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The kind of module source (currently local, git or dir).
    pub async fn kind(&self) -> Result<ModuleSourceKind, DaggerError> {
        let query = self.selection.select("kind");
        query.execute(self.graphql_client.clone()).await
    }
    /// The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.
    pub async fn local_context_directory_path(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("localContextDirectoryPath");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the module, including any setting via the withName API.
    pub async fn module_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("moduleName");
        query.execute(self.graphql_client.clone()).await
    }
    /// The original name of the module as read from the module's dagger.json (or set for the first time with the withName API).
    pub async fn module_original_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("moduleOriginalName");
        query.execute(self.graphql_client.clone()).await
    }
    /// The original subpath used when instantiating this module source, relative to the context directory.
    pub async fn original_subpath(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("originalSubpath");
        query.execute(self.graphql_client.clone()).await
    }
    /// The pinned version of this module source.
    pub async fn pin(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("pin");
        query.execute(self.graphql_client.clone()).await
    }
    /// The import path corresponding to the root of the git repo this source points to. Only valid for git sources.
    pub async fn repo_root_path(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("repoRootPath");
        query.execute(self.graphql_client.clone()).await
    }
    /// The SDK configuration of the module.
    pub fn sdk(&self) -> SdkConfig {
        let query = self.selection.select("sdk");
        SdkConfig {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The path, relative to the context directory, that contains the module's dagger.json.
    pub async fn source_root_subpath(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceRootSubpath");
        query.execute(self.graphql_client.clone()).await
    }
    /// The path to the directory containing the module's source code, relative to the context directory.
    pub async fn source_subpath(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceSubpath");
        query.execute(self.graphql_client.clone()).await
    }
    /// Forces evaluation of the module source, including any loading into the engine and associated validation.
    pub async fn sync(&self) -> Result<ModuleSourceId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// The specified version of the git repo this source points to.
    pub async fn version(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("version");
        query.execute(self.graphql_client.clone()).await
    }
    /// Update the module source with a new client to generate.
    ///
    /// # Arguments
    ///
    /// * `generator` - The generator to use
    /// * `output_dir` - The output directory for the generated client.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_client(
        &self,
        generator: impl Into<String>,
        output_dir: impl Into<String>,
    ) -> ModuleSource {
        let mut query = self.selection.select("withClient");
        query = query.arg("generator", generator.into());
        query = query.arg("outputDir", output_dir.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update the module source with a new client to generate.
    ///
    /// # Arguments
    ///
    /// * `generator` - The generator to use
    /// * `output_dir` - The output directory for the generated client.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_client_opts(
        &self,
        generator: impl Into<String>,
        output_dir: impl Into<String>,
        opts: ModuleSourceWithClientOpts,
    ) -> ModuleSource {
        let mut query = self.selection.select("withClient");
        query = query.arg("generator", generator.into());
        query = query.arg("outputDir", output_dir.into());
        if let Some(dev) = opts.dev {
            query = query.arg("dev", dev);
        }
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Append the provided dependencies to the module source's dependency list.
    ///
    /// # Arguments
    ///
    /// * `dependencies` - The dependencies to append.
    pub fn with_dependencies(&self, dependencies: Vec<ModuleSourceId>) -> ModuleSource {
        let mut query = self.selection.select("withDependencies");
        query = query.arg("dependencies", dependencies);
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Upgrade the engine version of the module to the given value.
    ///
    /// # Arguments
    ///
    /// * `version` - The engine version to upgrade to.
    pub fn with_engine_version(&self, version: impl Into<String>) -> ModuleSource {
        let mut query = self.selection.select("withEngineVersion");
        query = query.arg("version", version.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update the module source with additional include patterns for files+directories from its context that are required for building it
    ///
    /// # Arguments
    ///
    /// * `patterns` - The new additional include patterns.
    pub fn with_includes(&self, patterns: Vec<impl Into<String>>) -> ModuleSource {
        let mut query = self.selection.select("withIncludes");
        query = query.arg(
            "patterns",
            patterns
                .into_iter()
                .map(|i| i.into())
                .collect::<Vec<String>>(),
        );
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update the module source with a new name.
    ///
    /// # Arguments
    ///
    /// * `name` - The name to set.
    pub fn with_name(&self, name: impl Into<String>) -> ModuleSource {
        let mut query = self.selection.select("withName");
        query = query.arg("name", name.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update the module source with a new SDK.
    ///
    /// # Arguments
    ///
    /// * `source` - The SDK source to set.
    pub fn with_sdk(&self, source: impl Into<String>) -> ModuleSource {
        let mut query = self.selection.select("withSDK");
        query = query.arg("source", source.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update the module source with a new source subpath.
    ///
    /// # Arguments
    ///
    /// * `path` - The path to set as the source subpath. Must be relative to the module source's source root directory.
    pub fn with_source_subpath(&self, path: impl Into<String>) -> ModuleSource {
        let mut query = self.selection.select("withSourceSubpath");
        query = query.arg("path", path.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Update one or more module dependencies.
    ///
    /// # Arguments
    ///
    /// * `dependencies` - The dependencies to update.
    pub fn with_update_dependencies(&self, dependencies: Vec<impl Into<String>>) -> ModuleSource {
        let mut query = self.selection.select("withUpdateDependencies");
        query = query.arg(
            "dependencies",
            dependencies
                .into_iter()
                .map(|i| i.into())
                .collect::<Vec<String>>(),
        );
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Remove the provided dependencies from the module source's dependency list.
    ///
    /// # Arguments
    ///
    /// * `dependencies` - The dependencies to remove.
    pub fn without_dependencies(&self, dependencies: Vec<impl Into<String>>) -> ModuleSource {
        let mut query = self.selection.select("withoutDependencies");
        query = query.arg(
            "dependencies",
            dependencies
                .into_iter()
                .map(|i| i.into())
                .collect::<Vec<String>>(),
        );
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct ObjectTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ObjectTypeDef {
    /// The function used to construct new instances of this object, if any
    pub fn constructor(&self) -> Function {
        let query = self.selection.select("constructor");
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The doc string for the object, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Static fields defined on this object, if any.
    pub fn fields(&self) -> Vec<FieldTypeDef> {
        let query = self.selection.select("fields");
        vec![FieldTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Functions defined on this object, if any.
    pub fn functions(&self) -> Vec<Function> {
        let query = self.selection.select("functions");
        vec![Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// A unique identifier for this ObjectTypeDef.
    pub async fn id(&self) -> Result<ObjectTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the object.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The location of this object declaration.
    pub fn source_map(&self) -> SourceMap {
        let query = self.selection.select("sourceMap");
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.
    pub async fn source_module_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Port {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Port {
    /// The port description.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Skip the health check when run as a service.
    pub async fn experimental_skip_healthcheck(&self) -> Result<bool, DaggerError> {
        let query = self.selection.select("experimentalSkipHealthcheck");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this Port.
    pub async fn id(&self) -> Result<PortId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The port number.
    pub async fn port(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("port");
        query.execute(self.graphql_client.clone()).await
    }
    /// The transport layer protocol.
    pub async fn protocol(&self) -> Result<NetworkProtocol, DaggerError> {
        let query = self.selection.select("protocol");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Query {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryCacheVolumeOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub namespace: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryContainerOpts {
    /// Platform to initialize the container with.
    #[builder(setter(into, strip_option), default)]
    pub platform: Option<Platform>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryEnvOpts {
    /// Give the environment the same privileges as the caller: core API including host access, current module, and dependencies
    #[builder(setter(into, strip_option), default)]
    pub privileged: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryGitOpts<'a> {
    /// A service which must be started before the repo is fetched.
    #[builder(setter(into, strip_option), default)]
    pub experimental_service_host: Option<ServiceId>,
    /// DEPRECATED: Set to true to keep .git directory.
    #[builder(setter(into, strip_option), default)]
    pub keep_git_dir: Option<bool>,
    /// Set SSH auth socket
    #[builder(setter(into, strip_option), default)]
    pub ssh_auth_socket: Option<SocketId>,
    /// Set SSH known hosts
    #[builder(setter(into, strip_option), default)]
    pub ssh_known_hosts: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryHttpOpts {
    /// A service which must be started before the URL is fetched.
    #[builder(setter(into, strip_option), default)]
    pub experimental_service_host: Option<ServiceId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryLlmOpts<'a> {
    /// Cap the number of API calls for this LLM
    #[builder(setter(into, strip_option), default)]
    pub max_api_calls: Option<isize>,
    /// Model to use
    #[builder(setter(into, strip_option), default)]
    pub model: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryLoadSecretFromNameOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub accessor: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryModuleSourceOpts<'a> {
    /// If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.
    #[builder(setter(into, strip_option), default)]
    pub allow_not_exists: Option<bool>,
    /// If true, do not attempt to find dagger.json in a parent directory of the provided path. Only relevant for local module sources.
    #[builder(setter(into, strip_option), default)]
    pub disable_find_up: Option<bool>,
    /// The pinned version of the module source
    #[builder(setter(into, strip_option), default)]
    pub ref_pin: Option<&'a str>,
    /// If set, error out if the ref string is not of the provided requireKind.
    #[builder(setter(into, strip_option), default)]
    pub require_kind: Option<ModuleSourceKind>,
}
impl Query {
    /// Constructs a cache volume for a given cache key.
    ///
    /// # Arguments
    ///
    /// * `key` - A string identifier to target this cache volume (e.g., "modules-cache").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn cache_volume(&self, key: impl Into<String>) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");
        query = query.arg("key", key.into());
        CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Constructs a cache volume for a given cache key.
    ///
    /// # Arguments
    ///
    /// * `key` - A string identifier to target this cache volume (e.g., "modules-cache").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn cache_volume_opts<'a>(
        &self,
        key: impl Into<String>,
        opts: QueryCacheVolumeOpts<'a>,
    ) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");
        query = query.arg("key", key.into());
        if let Some(namespace) = opts.namespace {
            query = query.arg("namespace", namespace);
        }
        CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a scratch container.
    /// Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn container(&self) -> Container {
        let query = self.selection.select("container");
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a scratch container.
    /// Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn container_opts(&self, opts: QueryContainerOpts) -> Container {
        let mut query = self.selection.select("container");
        if let Some(platform) = opts.platform {
            query = query.arg("platform", platform);
        }
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The FunctionCall context that the SDK caller is currently executing in.
    /// If the caller is not currently executing in a function, this will return an error.
    pub fn current_function_call(&self) -> FunctionCall {
        let query = self.selection.select("currentFunctionCall");
        FunctionCall {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The module currently being served in the session, if any.
    pub fn current_module(&self) -> CurrentModule {
        let query = self.selection.select("currentModule");
        CurrentModule {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The TypeDef representations of the objects currently being served in the session.
    pub fn current_type_defs(&self) -> Vec<TypeDef> {
        let query = self.selection.select("currentTypeDefs");
        vec![TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// The default platform of the engine.
    pub async fn default_platform(&self) -> Result<Platform, DaggerError> {
        let query = self.selection.select("defaultPlatform");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates an empty directory.
    pub fn directory(&self) -> Directory {
        let query = self.selection.select("directory");
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// The Dagger engine container configuration and state
    pub fn engine(&self) -> Engine {
        let query = self.selection.select("engine");
        Engine {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initialize a new environment
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn env(&self) -> Env {
        let query = self.selection.select("env");
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initialize a new environment
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn env_opts(&self, opts: QueryEnvOpts) -> Env {
        let mut query = self.selection.select("env");
        if let Some(privileged) = opts.privileged {
            query = query.arg("privileged", privileged);
        }
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a new error.
    ///
    /// # Arguments
    ///
    /// * `message` - A brief description of the error.
    pub fn error(&self, message: impl Into<String>) -> Error {
        let mut query = self.selection.select("error");
        query = query.arg("message", message.into());
        Error {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a function.
    ///
    /// # Arguments
    ///
    /// * `name` - Name of the function, in its original format from the implementation language.
    /// * `return_type` - Return type of the function.
    pub fn function(
        &self,
        name: impl Into<String>,
        return_type: impl IntoID<TypeDefId>,
    ) -> Function {
        let mut query = self.selection.select("function");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "returnType",
            Box::new(move || {
                let return_type = return_type.clone();
                Box::pin(async move { return_type.into_id().await.unwrap().quote() })
            }),
        );
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a code generation result, given a directory containing the generated code.
    pub fn generated_code(&self, code: impl IntoID<DirectoryId>) -> GeneratedCode {
        let mut query = self.selection.select("generatedCode");
        query = query.arg_lazy(
            "code",
            Box::new(move || {
                let code = code.clone();
                Box::pin(async move { code.into_id().await.unwrap().quote() })
            }),
        );
        GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Queries a Git repository.
    ///
    /// # Arguments
    ///
    /// * `url` - URL of the git repository.
    ///
    /// Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.
    ///
    /// Suffix ".git" is optional.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git(&self, url: impl Into<String>) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Queries a Git repository.
    ///
    /// # Arguments
    ///
    /// * `url` - URL of the git repository.
    ///
    /// Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.
    ///
    /// Suffix ".git" is optional.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git_opts<'a>(&self, url: impl Into<String>, opts: QueryGitOpts<'a>) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        if let Some(keep_git_dir) = opts.keep_git_dir {
            query = query.arg("keepGitDir", keep_git_dir);
        }
        if let Some(experimental_service_host) = opts.experimental_service_host {
            query = query.arg("experimentalServiceHost", experimental_service_host);
        }
        if let Some(ssh_known_hosts) = opts.ssh_known_hosts {
            query = query.arg("sshKnownHosts", ssh_known_hosts);
        }
        if let Some(ssh_auth_socket) = opts.ssh_auth_socket {
            query = query.arg("sshAuthSocket", ssh_auth_socket);
        }
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Queries the host environment.
    pub fn host(&self) -> Host {
        let query = self.selection.select("host");
        Host {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a file containing an http remote url content.
    ///
    /// # Arguments
    ///
    /// * `url` - HTTP url to get the content from (e.g., "https://docs.dagger.io").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn http(&self, url: impl Into<String>) -> File {
        let mut query = self.selection.select("http");
        query = query.arg("url", url.into());
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a file containing an http remote url content.
    ///
    /// # Arguments
    ///
    /// * `url` - HTTP url to get the content from (e.g., "https://docs.dagger.io").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn http_opts(&self, url: impl Into<String>, opts: QueryHttpOpts) -> File {
        let mut query = self.selection.select("http");
        query = query.arg("url", url.into());
        if let Some(experimental_service_host) = opts.experimental_service_host {
            query = query.arg("experimentalServiceHost", experimental_service_host);
        }
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initialize a Large Language Model (LLM)
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn llm(&self) -> Llm {
        let query = self.selection.select("llm");
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Initialize a Large Language Model (LLM)
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn llm_opts<'a>(&self, opts: QueryLlmOpts<'a>) -> Llm {
        let mut query = self.selection.select("llm");
        if let Some(model) = opts.model {
            query = query.arg("model", model);
        }
        if let Some(max_api_calls) = opts.max_api_calls {
            query = query.arg("maxAPICalls", max_api_calls);
        }
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Binding from its ID.
    pub fn load_binding_from_id(&self, id: impl IntoID<BindingId>) -> Binding {
        let mut query = self.selection.select("loadBindingFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Binding {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a CacheVolume from its ID.
    pub fn load_cache_volume_from_id(&self, id: impl IntoID<CacheVolumeId>) -> CacheVolume {
        let mut query = self.selection.select("loadCacheVolumeFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Container from its ID.
    pub fn load_container_from_id(&self, id: impl IntoID<ContainerId>) -> Container {
        let mut query = self.selection.select("loadContainerFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a CurrentModule from its ID.
    pub fn load_current_module_from_id(&self, id: impl IntoID<CurrentModuleId>) -> CurrentModule {
        let mut query = self.selection.select("loadCurrentModuleFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        CurrentModule {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Directory from its ID.
    pub fn load_directory_from_id(&self, id: impl IntoID<DirectoryId>) -> Directory {
        let mut query = self.selection.select("loadDirectoryFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EngineCacheEntry from its ID.
    pub fn load_engine_cache_entry_from_id(
        &self,
        id: impl IntoID<EngineCacheEntryId>,
    ) -> EngineCacheEntry {
        let mut query = self.selection.select("loadEngineCacheEntryFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EngineCacheEntry {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EngineCacheEntrySet from its ID.
    pub fn load_engine_cache_entry_set_from_id(
        &self,
        id: impl IntoID<EngineCacheEntrySetId>,
    ) -> EngineCacheEntrySet {
        let mut query = self.selection.select("loadEngineCacheEntrySetFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EngineCacheEntrySet {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EngineCache from its ID.
    pub fn load_engine_cache_from_id(&self, id: impl IntoID<EngineCacheId>) -> EngineCache {
        let mut query = self.selection.select("loadEngineCacheFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EngineCache {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Engine from its ID.
    pub fn load_engine_from_id(&self, id: impl IntoID<EngineId>) -> Engine {
        let mut query = self.selection.select("loadEngineFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Engine {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EnumTypeDef from its ID.
    pub fn load_enum_type_def_from_id(&self, id: impl IntoID<EnumTypeDefId>) -> EnumTypeDef {
        let mut query = self.selection.select("loadEnumTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EnumTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EnumValueTypeDef from its ID.
    pub fn load_enum_value_type_def_from_id(
        &self,
        id: impl IntoID<EnumValueTypeDefId>,
    ) -> EnumValueTypeDef {
        let mut query = self.selection.select("loadEnumValueTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EnumValueTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Env from its ID.
    pub fn load_env_from_id(&self, id: impl IntoID<EnvId>) -> Env {
        let mut query = self.selection.select("loadEnvFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Env {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a EnvVariable from its ID.
    pub fn load_env_variable_from_id(&self, id: impl IntoID<EnvVariableId>) -> EnvVariable {
        let mut query = self.selection.select("loadEnvVariableFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Error from its ID.
    pub fn load_error_from_id(&self, id: impl IntoID<ErrorId>) -> Error {
        let mut query = self.selection.select("loadErrorFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Error {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ErrorValue from its ID.
    pub fn load_error_value_from_id(&self, id: impl IntoID<ErrorValueId>) -> ErrorValue {
        let mut query = self.selection.select("loadErrorValueFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ErrorValue {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a FieldTypeDef from its ID.
    pub fn load_field_type_def_from_id(&self, id: impl IntoID<FieldTypeDefId>) -> FieldTypeDef {
        let mut query = self.selection.select("loadFieldTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        FieldTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a File from its ID.
    pub fn load_file_from_id(&self, id: impl IntoID<FileId>) -> File {
        let mut query = self.selection.select("loadFileFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a FunctionArg from its ID.
    pub fn load_function_arg_from_id(&self, id: impl IntoID<FunctionArgId>) -> FunctionArg {
        let mut query = self.selection.select("loadFunctionArgFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        FunctionArg {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a FunctionCallArgValue from its ID.
    pub fn load_function_call_arg_value_from_id(
        &self,
        id: impl IntoID<FunctionCallArgValueId>,
    ) -> FunctionCallArgValue {
        let mut query = self.selection.select("loadFunctionCallArgValueFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        FunctionCallArgValue {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a FunctionCall from its ID.
    pub fn load_function_call_from_id(&self, id: impl IntoID<FunctionCallId>) -> FunctionCall {
        let mut query = self.selection.select("loadFunctionCallFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        FunctionCall {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Function from its ID.
    pub fn load_function_from_id(&self, id: impl IntoID<FunctionId>) -> Function {
        let mut query = self.selection.select("loadFunctionFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a GeneratedCode from its ID.
    pub fn load_generated_code_from_id(&self, id: impl IntoID<GeneratedCodeId>) -> GeneratedCode {
        let mut query = self.selection.select("loadGeneratedCodeFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a GitRef from its ID.
    pub fn load_git_ref_from_id(&self, id: impl IntoID<GitRefId>) -> GitRef {
        let mut query = self.selection.select("loadGitRefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a GitRepository from its ID.
    pub fn load_git_repository_from_id(&self, id: impl IntoID<GitRepositoryId>) -> GitRepository {
        let mut query = self.selection.select("loadGitRepositoryFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Host from its ID.
    pub fn load_host_from_id(&self, id: impl IntoID<HostId>) -> Host {
        let mut query = self.selection.select("loadHostFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Host {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a InputTypeDef from its ID.
    pub fn load_input_type_def_from_id(&self, id: impl IntoID<InputTypeDefId>) -> InputTypeDef {
        let mut query = self.selection.select("loadInputTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        InputTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a InterfaceTypeDef from its ID.
    pub fn load_interface_type_def_from_id(
        &self,
        id: impl IntoID<InterfaceTypeDefId>,
    ) -> InterfaceTypeDef {
        let mut query = self.selection.select("loadInterfaceTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        InterfaceTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a LLM from its ID.
    pub fn load_llm_from_id(&self, id: impl IntoID<Llmid>) -> Llm {
        let mut query = self.selection.select("loadLLMFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Llm {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a LLMTokenUsage from its ID.
    pub fn load_llm_token_usage_from_id(&self, id: impl IntoID<LlmTokenUsageId>) -> LlmTokenUsage {
        let mut query = self.selection.select("loadLLMTokenUsageFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        LlmTokenUsage {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Label from its ID.
    pub fn load_label_from_id(&self, id: impl IntoID<LabelId>) -> Label {
        let mut query = self.selection.select("loadLabelFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Label {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ListTypeDef from its ID.
    pub fn load_list_type_def_from_id(&self, id: impl IntoID<ListTypeDefId>) -> ListTypeDef {
        let mut query = self.selection.select("loadListTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ListTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ModuleConfigClient from its ID.
    pub fn load_module_config_client_from_id(
        &self,
        id: impl IntoID<ModuleConfigClientId>,
    ) -> ModuleConfigClient {
        let mut query = self.selection.select("loadModuleConfigClientFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ModuleConfigClient {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Module from its ID.
    pub fn load_module_from_id(&self, id: impl IntoID<ModuleId>) -> Module {
        let mut query = self.selection.select("loadModuleFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ModuleSource from its ID.
    pub fn load_module_source_from_id(&self, id: impl IntoID<ModuleSourceId>) -> ModuleSource {
        let mut query = self.selection.select("loadModuleSourceFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ObjectTypeDef from its ID.
    pub fn load_object_type_def_from_id(&self, id: impl IntoID<ObjectTypeDefId>) -> ObjectTypeDef {
        let mut query = self.selection.select("loadObjectTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ObjectTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Port from its ID.
    pub fn load_port_from_id(&self, id: impl IntoID<PortId>) -> Port {
        let mut query = self.selection.select("loadPortFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Port {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a SDKConfig from its ID.
    pub fn load_sdk_config_from_id(&self, id: impl IntoID<SdkConfigId>) -> SdkConfig {
        let mut query = self.selection.select("loadSDKConfigFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        SdkConfig {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a ScalarTypeDef from its ID.
    pub fn load_scalar_type_def_from_id(&self, id: impl IntoID<ScalarTypeDefId>) -> ScalarTypeDef {
        let mut query = self.selection.select("loadScalarTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        ScalarTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Secret from its ID.
    pub fn load_secret_from_id(&self, id: impl IntoID<SecretId>) -> Secret {
        let mut query = self.selection.select("loadSecretFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Secret from its Name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn load_secret_from_name(&self, name: impl Into<String>) -> Secret {
        let mut query = self.selection.select("loadSecretFromName");
        query = query.arg("name", name.into());
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Secret from its Name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn load_secret_from_name_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: QueryLoadSecretFromNameOpts<'a>,
    ) -> Secret {
        let mut query = self.selection.select("loadSecretFromName");
        query = query.arg("name", name.into());
        if let Some(accessor) = opts.accessor {
            query = query.arg("accessor", accessor);
        }
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Service from its ID.
    pub fn load_service_from_id(&self, id: impl IntoID<ServiceId>) -> Service {
        let mut query = self.selection.select("loadServiceFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Socket from its ID.
    pub fn load_socket_from_id(&self, id: impl IntoID<SocketId>) -> Socket {
        let mut query = self.selection.select("loadSocketFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a SourceMap from its ID.
    pub fn load_source_map_from_id(&self, id: impl IntoID<SourceMapId>) -> SourceMap {
        let mut query = self.selection.select("loadSourceMapFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a Terminal from its ID.
    pub fn load_terminal_from_id(&self, id: impl IntoID<TerminalId>) -> Terminal {
        let mut query = self.selection.select("loadTerminalFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        Terminal {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Load a TypeDef from its ID.
    pub fn load_type_def_from_id(&self, id: impl IntoID<TypeDefId>) -> TypeDef {
        let mut query = self.selection.select("loadTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.into_id().await.unwrap().quote() })
            }),
        );
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a new module.
    pub fn module(&self) -> Module {
        let query = self.selection.select("module");
        Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a new module source instance from a source ref string
    ///
    /// # Arguments
    ///
    /// * `ref_string` - The string ref representation of the module source
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn module_source(&self, ref_string: impl Into<String>) -> ModuleSource {
        let mut query = self.selection.select("moduleSource");
        query = query.arg("refString", ref_string.into());
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a new module source instance from a source ref string
    ///
    /// # Arguments
    ///
    /// * `ref_string` - The string ref representation of the module source
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn module_source_opts<'a>(
        &self,
        ref_string: impl Into<String>,
        opts: QueryModuleSourceOpts<'a>,
    ) -> ModuleSource {
        let mut query = self.selection.select("moduleSource");
        query = query.arg("refString", ref_string.into());
        if let Some(ref_pin) = opts.ref_pin {
            query = query.arg("refPin", ref_pin);
        }
        if let Some(disable_find_up) = opts.disable_find_up {
            query = query.arg("disableFindUp", disable_find_up);
        }
        if let Some(allow_not_exists) = opts.allow_not_exists {
            query = query.arg("allowNotExists", allow_not_exists);
        }
        if let Some(require_kind) = opts.require_kind {
            query = query.arg("requireKind", require_kind);
        }
        ModuleSource {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates a new secret.
    ///
    /// # Arguments
    ///
    /// * `uri` - The URI of the secret store
    pub fn secret(&self, uri: impl Into<String>) -> Secret {
        let mut query = self.selection.select("secret");
        query = query.arg("uri", uri.into());
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Sets a secret given a user defined name to its plaintext and returns the secret.
    /// The plaintext value is limited to a size of 128000 bytes.
    ///
    /// # Arguments
    ///
    /// * `name` - The user defined name for this secret
    /// * `plaintext` - The plaintext of the secret
    pub fn set_secret(&self, name: impl Into<String>, plaintext: impl Into<String>) -> Secret {
        let mut query = self.selection.select("setSecret");
        query = query.arg("name", name.into());
        query = query.arg("plaintext", plaintext.into());
        Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Creates source map metadata.
    ///
    /// # Arguments
    ///
    /// * `filename` - The filename from the module source.
    /// * `line` - The line number within the filename.
    /// * `column` - The column number within the line.
    pub fn source_map(&self, filename: impl Into<String>, line: isize, column: isize) -> SourceMap {
        let mut query = self.selection.select("sourceMap");
        query = query.arg("filename", filename.into());
        query = query.arg("line", line);
        query = query.arg("column", column);
        SourceMap {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Create a new TypeDef.
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Get the current Dagger Engine version.
    pub async fn version(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("version");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct SdkConfig {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl SdkConfig {
    /// A unique identifier for this SDKConfig.
    pub async fn id(&self) -> Result<SdkConfigId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.
    pub async fn source(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("source");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct ScalarTypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ScalarTypeDef {
    /// A doc string for the scalar, if any.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this ScalarTypeDef.
    pub async fn id(&self) -> Result<ScalarTypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the scalar.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.
    pub async fn source_module_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceModuleName");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Secret {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Secret {
    /// A unique identifier for this Secret.
    pub async fn id(&self) -> Result<SecretId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of this secret.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of this secret.
    pub async fn plaintext(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("plaintext");
        query.execute(self.graphql_client.clone()).await
    }
    /// The URI of this secret.
    pub async fn uri(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("uri");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Service {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ServiceEndpointOpts<'a> {
    /// The exposed port number for the endpoint
    #[builder(setter(into, strip_option), default)]
    pub port: Option<isize>,
    /// Return a URL with the given scheme, eg. http for http://
    #[builder(setter(into, strip_option), default)]
    pub scheme: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ServiceStopOpts {
    /// Immediately kill the service without waiting for a graceful exit
    #[builder(setter(into, strip_option), default)]
    pub kill: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ServiceUpOpts {
    /// List of frontend/backend port mappings to forward.
    /// Frontend is the port accepting traffic on the host, backend is the service port.
    #[builder(setter(into, strip_option), default)]
    pub ports: Option<Vec<PortForward>>,
    /// Bind each tunnel port to a random port on the host.
    #[builder(setter(into, strip_option), default)]
    pub random: Option<bool>,
}
impl Service {
    /// Retrieves an endpoint that clients can use to reach this container.
    /// If no port is specified, the first exposed port is used. If none exist an error is returned.
    /// If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn endpoint(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("endpoint");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves an endpoint that clients can use to reach this container.
    /// If no port is specified, the first exposed port is used. If none exist an error is returned.
    /// If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn endpoint_opts<'a>(
        &self,
        opts: ServiceEndpointOpts<'a>,
    ) -> Result<String, DaggerError> {
        let mut query = self.selection.select("endpoint");
        if let Some(port) = opts.port {
            query = query.arg("port", port);
        }
        if let Some(scheme) = opts.scheme {
            query = query.arg("scheme", scheme);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves a hostname which can be used by clients to reach this container.
    pub async fn hostname(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("hostname");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this Service.
    pub async fn id(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of ports provided by the service.
    pub fn ports(&self) -> Vec<Port> {
        let query = self.selection.select("ports");
        vec![Port {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }]
    }
    /// Start the service and wait for its health checks to succeed.
    /// Services bound to a Container do not need to be manually started.
    pub async fn start(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("start");
        query.execute(self.graphql_client.clone()).await
    }
    /// Stop the service.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn stop(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("stop");
        query.execute(self.graphql_client.clone()).await
    }
    /// Stop the service.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn stop_opts(&self, opts: ServiceStopOpts) -> Result<ServiceId, DaggerError> {
        let mut query = self.selection.select("stop");
        if let Some(kill) = opts.kill {
            query = query.arg("kill", kill);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a tunnel that forwards traffic from the caller's network to this service.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn up(&self) -> Result<Void, DaggerError> {
        let query = self.selection.select("up");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a tunnel that forwards traffic from the caller's network to this service.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn up_opts(&self, opts: ServiceUpOpts) -> Result<Void, DaggerError> {
        let mut query = self.selection.select("up");
        if let Some(ports) = opts.ports {
            query = query.arg("ports", ports);
        }
        if let Some(random) = opts.random {
            query = query.arg("random", random);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Configures a hostname which can be used by clients within the session to reach this container.
    ///
    /// # Arguments
    ///
    /// * `hostname` - The hostname to use.
    pub fn with_hostname(&self, hostname: impl Into<String>) -> Service {
        let mut query = self.selection.select("withHostname");
        query = query.arg("hostname", hostname.into());
        Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Clone)]
pub struct Socket {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Socket {
    /// A unique identifier for this Socket.
    pub async fn id(&self) -> Result<SocketId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct SourceMap {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl SourceMap {
    /// The column number within the line.
    pub async fn column(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("column");
        query.execute(self.graphql_client.clone()).await
    }
    /// The filename from the module source.
    pub async fn filename(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("filename");
        query.execute(self.graphql_client.clone()).await
    }
    /// A unique identifier for this SourceMap.
    pub async fn id(&self) -> Result<SourceMapId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The line number within the filename.
    pub async fn line(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("line");
        query.execute(self.graphql_client.clone()).await
    }
    /// The module dependency this was declared in.
    pub async fn module(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("module");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Terminal {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Terminal {
    /// A unique identifier for this Terminal.
    pub async fn id(&self) -> Result<TerminalId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Forces evaluation of the pipeline in the engine.
    /// It doesn't run the default command if no exec has been set.
    pub async fn sync(&self) -> Result<TerminalId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct TypeDef {
    pub proc: Option<Arc<DaggerSessionProc>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithEnumOpts<'a> {
    /// A doc string for the enum, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// The source map for the enum definition.
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithEnumValueOpts<'a> {
    /// A doc string for the value, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// The source map for the enum value definition.
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithFieldOpts<'a> {
    /// A doc string for the field, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// The source map for the field definition.
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithInterfaceOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithObjectOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    #[builder(setter(into, strip_option), default)]
    pub source_map: Option<SourceMapId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithScalarOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
}
impl TypeDef {
    /// If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.
    pub fn as_enum(&self) -> EnumTypeDef {
        let query = self.selection.select("asEnum");
        EnumTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.
    pub fn as_input(&self) -> InputTypeDef {
        let query = self.selection.select("asInput");
        InputTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.
    pub fn as_interface(&self) -> InterfaceTypeDef {
        let query = self.selection.select("asInterface");
        InterfaceTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.
    pub fn as_list(&self) -> ListTypeDef {
        let query = self.selection.select("asList");
        ListTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.
    pub fn as_object(&self) -> ObjectTypeDef {
        let query = self.selection.select("asObject");
        ObjectTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.
    pub fn as_scalar(&self) -> ScalarTypeDef {
        let query = self.selection.select("asScalar");
        ScalarTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// A unique identifier for this TypeDef.
    pub async fn id(&self) -> Result<TypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The kind of type this is (e.g. primitive, list, object).
    pub async fn kind(&self) -> Result<TypeDefKind, DaggerError> {
        let query = self.selection.select("kind");
        query.execute(self.graphql_client.clone()).await
    }
    /// Whether this type can be set to null. Defaults to false.
    pub async fn optional(&self) -> Result<bool, DaggerError> {
        let query = self.selection.select("optional");
        query.execute(self.graphql_client.clone()).await
    }
    /// Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.
    pub fn with_constructor(&self, function: impl IntoID<FunctionId>) -> TypeDef {
        let mut query = self.selection.select("withConstructor");
        query = query.arg_lazy(
            "function",
            Box::new(move || {
                let function = function.clone();
                Box::pin(async move { function.into_id().await.unwrap().quote() })
            }),
        );
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Enum with the provided name.
    /// Note that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the enum
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_enum(&self, name: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withEnum");
        query = query.arg("name", name.into());
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Enum with the provided name.
    /// Note that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the enum
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_enum_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: TypeDefWithEnumOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withEnum");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Adds a static value for an Enum TypeDef, failing if the type is not an enum.
    ///
    /// # Arguments
    ///
    /// * `value` - The name of the value in the enum
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_enum_value(&self, value: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withEnumValue");
        query = query.arg("value", value.into());
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Adds a static value for an Enum TypeDef, failing if the type is not an enum.
    ///
    /// # Arguments
    ///
    /// * `value` - The name of the value in the enum
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_enum_value_opts<'a>(
        &self,
        value: impl Into<String>,
        opts: TypeDefWithEnumValueOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withEnumValue");
        query = query.arg("value", value.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Adds a static field for an Object TypeDef, failing if the type is not an object.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the field in the object
    /// * `type_def` - The type of the field
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_field(&self, name: impl Into<String>, type_def: impl IntoID<TypeDefId>) -> TypeDef {
        let mut query = self.selection.select("withField");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.into_id().await.unwrap().quote() })
            }),
        );
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Adds a static field for an Object TypeDef, failing if the type is not an object.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the field in the object
    /// * `type_def` - The type of the field
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_field_opts<'a>(
        &self,
        name: impl Into<String>,
        type_def: impl IntoID<TypeDefId>,
        opts: TypeDefWithFieldOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withField");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.into_id().await.unwrap().quote() })
            }),
        );
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.
    pub fn with_function(&self, function: impl IntoID<FunctionId>) -> TypeDef {
        let mut query = self.selection.select("withFunction");
        query = query.arg_lazy(
            "function",
            Box::new(move || {
                let function = function.clone();
                Box::pin(async move { function.into_id().await.unwrap().quote() })
            }),
        );
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Interface with the provided name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_interface(&self, name: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withInterface");
        query = query.arg("name", name.into());
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Interface with the provided name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_interface_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: TypeDefWithInterfaceOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withInterface");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Sets the kind of the type.
    pub fn with_kind(&self, kind: TypeDefKind) -> TypeDef {
        let mut query = self.selection.select("withKind");
        query = query.arg("kind", kind);
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind List with the provided type for its elements.
    pub fn with_list_of(&self, element_type: impl IntoID<TypeDefId>) -> TypeDef {
        let mut query = self.selection.select("withListOf");
        query = query.arg_lazy(
            "elementType",
            Box::new(move || {
                let element_type = element_type.clone();
                Box::pin(async move { element_type.into_id().await.unwrap().quote() })
            }),
        );
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Object with the provided name.
    /// Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_object(&self, name: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withObject");
        query = query.arg("name", name.into());
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Object with the provided name.
    /// Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_object_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: TypeDefWithObjectOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withObject");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(source_map) = opts.source_map {
            query = query.arg("sourceMap", source_map);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Sets whether this type can be set to null.
    pub fn with_optional(&self, optional: bool) -> TypeDef {
        let mut query = self.selection.select("withOptional");
        query = query.arg("optional", optional);
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Scalar with the provided name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_scalar(&self, name: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withScalar");
        query = query.arg("name", name.into());
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
    /// Returns a TypeDef of kind Scalar with the provided name.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_scalar_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: TypeDefWithScalarOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withScalar");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }
    }
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum CacheSharingMode {
    #[serde(rename = "LOCKED")]
    Locked,
    #[serde(rename = "PRIVATE")]
    Private,
    #[serde(rename = "SHARED")]
    Shared,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ImageLayerCompression {
    #[serde(rename = "EStarGZ")]
    EStarGz,
    #[serde(rename = "Gzip")]
    Gzip,
    #[serde(rename = "Uncompressed")]
    Uncompressed,
    #[serde(rename = "Zstd")]
    Zstd,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ImageMediaTypes {
    #[serde(rename = "DockerMediaTypes")]
    DockerMediaTypes,
    #[serde(rename = "OCIMediaTypes")]
    OciMediaTypes,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ModuleSourceKind {
    #[serde(rename = "DIR_SOURCE")]
    DirSource,
    #[serde(rename = "GIT_SOURCE")]
    GitSource,
    #[serde(rename = "LOCAL_SOURCE")]
    LocalSource,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum NetworkProtocol {
    #[serde(rename = "TCP")]
    Tcp,
    #[serde(rename = "UDP")]
    Udp,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ReturnType {
    #[serde(rename = "ANY")]
    Any,
    #[serde(rename = "FAILURE")]
    Failure,
    #[serde(rename = "SUCCESS")]
    Success,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum TypeDefKind {
    #[serde(rename = "BOOLEAN_KIND")]
    BooleanKind,
    #[serde(rename = "ENUM_KIND")]
    EnumKind,
    #[serde(rename = "FLOAT_KIND")]
    FloatKind,
    #[serde(rename = "INPUT_KIND")]
    InputKind,
    #[serde(rename = "INTEGER_KIND")]
    IntegerKind,
    #[serde(rename = "INTERFACE_KIND")]
    InterfaceKind,
    #[serde(rename = "LIST_KIND")]
    ListKind,
    #[serde(rename = "OBJECT_KIND")]
    ObjectKind,
    #[serde(rename = "SCALAR_KIND")]
    ScalarKind,
    #[serde(rename = "STRING_KIND")]
    StringKind,
    #[serde(rename = "VOID_KIND")]
    VoidKind,
}
