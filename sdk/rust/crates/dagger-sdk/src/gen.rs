use crate::core::graphql_client::DynGraphQLClient;
use crate::errors::DaggerError;
use crate::querybuilder::Selection;
use derive_builder::Builder;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tokio::process::Child;

#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct CacheVolumeId(pub String);
impl Into<CacheVolumeId> for &str {
    fn into(self) -> CacheVolumeId {
        CacheVolumeId(self.to_string())
    }
}
impl Into<CacheVolumeId> for String {
    fn into(self) -> CacheVolumeId {
        CacheVolumeId(self.clone())
    }
}
impl CacheVolumeId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ContainerId(pub String);
impl Into<ContainerId> for &str {
    fn into(self) -> ContainerId {
        ContainerId(self.to_string())
    }
}
impl Into<ContainerId> for String {
    fn into(self) -> ContainerId {
        ContainerId(self.clone())
    }
}
impl ContainerId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct DirectoryId(pub String);
impl Into<DirectoryId> for &str {
    fn into(self) -> DirectoryId {
        DirectoryId(self.to_string())
    }
}
impl Into<DirectoryId> for String {
    fn into(self) -> DirectoryId {
        DirectoryId(self.clone())
    }
}
impl DirectoryId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FileId(pub String);
impl Into<FileId> for &str {
    fn into(self) -> FileId {
        FileId(self.to_string())
    }
}
impl Into<FileId> for String {
    fn into(self) -> FileId {
        FileId(self.clone())
    }
}
impl FileId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionArgId(pub String);
impl Into<FunctionArgId> for &str {
    fn into(self) -> FunctionArgId {
        FunctionArgId(self.to_string())
    }
}
impl Into<FunctionArgId> for String {
    fn into(self) -> FunctionArgId {
        FunctionArgId(self.clone())
    }
}
impl FunctionArgId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FunctionId(pub String);
impl Into<FunctionId> for &str {
    fn into(self) -> FunctionId {
        FunctionId(self.to_string())
    }
}
impl Into<FunctionId> for String {
    fn into(self) -> FunctionId {
        FunctionId(self.clone())
    }
}
impl FunctionId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GeneratedCodeId(pub String);
impl Into<GeneratedCodeId> for &str {
    fn into(self) -> GeneratedCodeId {
        GeneratedCodeId(self.to_string())
    }
}
impl Into<GeneratedCodeId> for String {
    fn into(self) -> GeneratedCodeId {
        GeneratedCodeId(self.clone())
    }
}
impl GeneratedCodeId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GitRefId(pub String);
impl Into<GitRefId> for &str {
    fn into(self) -> GitRefId {
        GitRefId(self.to_string())
    }
}
impl Into<GitRefId> for String {
    fn into(self) -> GitRefId {
        GitRefId(self.clone())
    }
}
impl GitRefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct GitRepositoryId(pub String);
impl Into<GitRepositoryId> for &str {
    fn into(self) -> GitRepositoryId {
        GitRepositoryId(self.to_string())
    }
}
impl Into<GitRepositoryId> for String {
    fn into(self) -> GitRepositoryId {
        GitRepositoryId(self.clone())
    }
}
impl GitRepositoryId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Json(pub String);
impl Into<Json> for &str {
    fn into(self) -> Json {
        Json(self.to_string())
    }
}
impl Into<Json> for String {
    fn into(self) -> Json {
        Json(self.clone())
    }
}
impl Json {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ModuleId(pub String);
impl Into<ModuleId> for &str {
    fn into(self) -> ModuleId {
        ModuleId(self.to_string())
    }
}
impl Into<ModuleId> for String {
    fn into(self) -> ModuleId {
        ModuleId(self.clone())
    }
}
impl ModuleId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Platform(pub String);
impl Into<Platform> for &str {
    fn into(self) -> Platform {
        Platform(self.to_string())
    }
}
impl Into<Platform> for String {
    fn into(self) -> Platform {
        Platform(self.clone())
    }
}
impl Platform {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SecretId(pub String);
impl Into<SecretId> for &str {
    fn into(self) -> SecretId {
        SecretId(self.to_string())
    }
}
impl Into<SecretId> for String {
    fn into(self) -> SecretId {
        SecretId(self.clone())
    }
}
impl SecretId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ServiceId(pub String);
impl Into<ServiceId> for &str {
    fn into(self) -> ServiceId {
        ServiceId(self.to_string())
    }
}
impl Into<ServiceId> for String {
    fn into(self) -> ServiceId {
        ServiceId(self.clone())
    }
}
impl ServiceId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SocketId(pub String);
impl Into<SocketId> for &str {
    fn into(self) -> SocketId {
        SocketId(self.to_string())
    }
}
impl Into<SocketId> for String {
    fn into(self) -> SocketId {
        SocketId(self.clone())
    }
}
impl SocketId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct TypeDefId(pub String);
impl Into<TypeDefId> for &str {
    fn into(self) -> TypeDefId {
        TypeDefId(self.to_string())
    }
}
impl Into<TypeDefId> for String {
    fn into(self) -> TypeDefId {
        TypeDefId(self.clone())
    }
}
impl TypeDefId {
    fn quote(&self) -> String {
        format!("\"{}\"", self.0.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Void(pub String);
impl Into<Void> for &str {
    fn into(self) -> Void {
        Void(self.to_string())
    }
}
impl Into<Void> for String {
    fn into(self) -> Void {
        Void(self.clone())
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
#[derive(Clone)]
pub struct CacheVolume {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl CacheVolume {
    pub async fn id(&self) -> Result<CacheVolumeId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Container {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerAsTarballOpts {
    /// Force each layer of the image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's
    /// cache, that will be used (this can result in a mix of compression algorithms for
    /// different layers). If this is unset and a layer has no compressed blob in the
    /// engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the image's layers. Defaults to OCI, which
    /// is largely compatible with most recent container runtimes, but Docker may be needed
    /// for older runtimes without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerBuildOpts<'a> {
    /// Additional build arguments.
    #[builder(setter(into, strip_option), default)]
    pub build_args: Option<Vec<BuildArg>>,
    /// Path to the Dockerfile to use.
    /// Default: './Dockerfile'.
    #[builder(setter(into, strip_option), default)]
    pub dockerfile: Option<&'a str>,
    /// Secrets to pass to the build.
    /// They will be mounted at /run/secrets/[secret-name] in the build container
    /// They can be accessed in the Dockerfile using the "secret" mount type
    /// and mount path /run/secrets/[secret-name]
    /// e.g. RUN --mount=type=secret,id=my-secret curl url?token=$(cat /run/secrets/my-secret)"
    #[builder(setter(into, strip_option), default)]
    pub secrets: Option<Vec<SecretId>>,
    /// Target build stage to build.
    #[builder(setter(into, strip_option), default)]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExportOpts {
    /// Force each layer of the exported image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's
    /// cache, that will be used (this can result in a mix of compression algorithms for
    /// different layers). If this is unset and a layer has no compressed blob in the
    /// engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the exported image's layers. Defaults to OCI, which
    /// is largely compatible with most recent container runtimes, but Docker may be needed
    /// for older runtimes without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerImportOpts<'a> {
    /// Identifies the tag to import from the archive, if the archive bundles
    /// multiple tags.
    #[builder(setter(into, strip_option), default)]
    pub tag: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerPipelineOpts<'a> {
    /// Pipeline description.
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Pipeline labels.
    #[builder(setter(into, strip_option), default)]
    pub labels: Option<Vec<PipelineLabel>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerPublishOpts {
    /// Force each layer of the published image to use the specified compression algorithm.
    /// If this is unset, then if a layer already has a compressed blob in the engine's
    /// cache, that will be used (this can result in a mix of compression algorithms for
    /// different layers). If this is unset and a layer has no compressed blob in the
    /// engine's cache, then it will be compressed using Gzip.
    #[builder(setter(into, strip_option), default)]
    pub forced_compression: Option<ImageLayerCompression>,
    /// Use the specified media types for the published image's layers. Defaults to OCI, which
    /// is largely compatible with most recent registries, but Docker may be needed for older
    /// registries without OCI support.
    #[builder(setter(into, strip_option), default)]
    pub media_types: Option<ImageMediaTypes>,
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option), default)]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDefaultArgsOpts<'a> {
    /// Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).
    #[builder(setter(into, strip_option), default)]
    pub args: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDirectoryOpts<'a> {
    /// Patterns to exclude in the written directory (e.g., ["node_modules/**", ".gitignore", ".git/"]).
    #[builder(setter(into, strip_option), default)]
    pub exclude: Option<Vec<&'a str>>,
    /// Patterns to include in the written directory (e.g., ["*.go", "go.mod", "go.sum"]).
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
    /// Replace `${VAR}` or $VAR in the value according to the current environment
    /// variables defined in the container (e.g., "/opt/bin:$PATH").
    #[builder(setter(into, strip_option), default)]
    pub expand: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithExecOpts<'a> {
    /// Provides dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed.
    /// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option), default)]
    pub experimental_privileged_nesting: Option<bool>,
    /// Execute the command with all root capabilities. This is similar to running a command
    /// with "sudo" or executing `docker run` with the `--privileged` flag. Containerization
    /// does not provide any security guarantees when using this option. It should only be used
    /// when absolutely necessary and only with trusted commands.
    #[builder(setter(into, strip_option), default)]
    pub insecure_root_capabilities: Option<bool>,
    /// Redirect the command's standard error to a file in the container (e.g., "/tmp/stderr").
    #[builder(setter(into, strip_option), default)]
    pub redirect_stderr: Option<&'a str>,
    /// Redirect the command's standard output to a file in the container (e.g., "/tmp/stdout").
    #[builder(setter(into, strip_option), default)]
    pub redirect_stdout: Option<&'a str>,
    /// If the container has an entrypoint, ignore it for args rather than using it to wrap them.
    #[builder(setter(into, strip_option), default)]
    pub skip_entrypoint: Option<bool>,
    /// Content to write to the command's standard input before closing (e.g., "Hello world").
    #[builder(setter(into, strip_option), default)]
    pub stdin: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithExposedPortOpts<'a> {
    /// Optional port description
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Transport layer network protocol
    #[builder(setter(into, strip_option), default)]
    pub protocol: Option<NetworkProtocol>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithFileOpts<'a> {
    /// A user:group to set for the file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Permission given to the copied file (e.g., 0600).
    /// Default: 0644.
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedCacheOpts<'a> {
    /// A user:group to set for the mounted cache directory.
    /// Note that this changes the ownership of the specified mount along with the
    /// initial filesystem provided by source (if any). It does not have any effect
    /// if/when the cache has already been created.
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
    /// A user:group to set for the mounted directory and its contents.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedFileOpts<'a> {
    /// A user or user:group to set for the mounted file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedSecretOpts<'a> {
    /// Permission given to the mounted secret (e.g., 0600).
    /// This option requires an owner to be set to be active.
    /// Default: 0400.
    #[builder(setter(into, strip_option), default)]
    pub mode: Option<isize>,
    /// A user:group to set for the mounted secret.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithNewFileOpts<'a> {
    /// Content of the file to write (e.g., "Hello world!").
    #[builder(setter(into, strip_option), default)]
    pub contents: Option<&'a str>,
    /// A user:group to set for the file.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
    /// Permission given to the written file (e.g., 0600).
    /// Default: 0644.
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithUnixSocketOpts<'a> {
    /// A user:group to set for the mounted socket.
    /// The user and group can either be an ID (1000:1000) or a name (foo:bar).
    /// If the group is omitted, it defaults to the same as the user.
    #[builder(setter(into, strip_option), default)]
    pub owner: Option<&'a str>,
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
impl Container {
    /// Turn the container into a Service.
    /// Be sure to set any exposed ports before this conversion.
    pub fn as_service(&self) -> Service {
        let query = self.selection.select("asService");
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns a File representing the container serialized to a tarball.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_tarball(&self) -> File {
        let query = self.selection.select("asTarball");
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
            query = query.arg_enum("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg_enum("mediaTypes", media_types);
        }
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Initializes this container from a Dockerfile build.
    ///
    /// # Arguments
    ///
    /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build(&self, context: Directory) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg_lazy(
            "context",
            Box::new(move || {
                let context = context.clone();
                Box::pin(async move { context.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Initializes this container from a Dockerfile build.
    ///
    /// # Arguments
    ///
    /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build_opts<'a>(&self, context: Directory, opts: ContainerBuildOpts<'a>) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg_lazy(
            "context",
            Box::new(move || {
                let context = context.clone();
                Box::pin(async move { context.id().await.unwrap().quote() })
            }),
        );
        if let Some(dockerfile) = opts.dockerfile {
            query = query.arg("dockerfile", dockerfile);
        }
        if let Some(build_args) = opts.build_args {
            query = query.arg("buildArgs", build_args);
        }
        if let Some(target) = opts.target {
            query = query.arg("target", target);
        }
        if let Some(secrets) = opts.secrets {
            query = query.arg("secrets", secrets);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return vec![EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    /// experimentalWithAllGPUs configures all available GPUs on the host to be accessible to this container.
    /// This currently works for Nvidia devices only.
    pub fn experimental_with_all_gp_us(&self) -> Container {
        let query = self.selection.select("experimentalWithAllGPUs");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// EXPERIMENTAL API! Subject to change/removal at any time.
    /// experimentalWithGPU configures the provided list of devices to be accesible to this container.
    /// This currently works for Nvidia devices only.
    pub fn experimental_with_gpu(&self, devices: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("experimentalWithGPU");
        query = query.arg(
            "devices",
            devices
                .into_iter()
                .map(|i| i.into())
                .collect::<Vec<String>>(),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Writes the container as an OCI tarball to the destination file path on the host for the specified platform variants.
    /// Return true on success.
    /// It can also publishes platform variants.
    ///
    /// # Arguments
    ///
    /// * `path` - Host's destination path (e.g., "./tarball").
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> Result<bool, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the container as an OCI tarball to the destination file path on the host for the specified platform variants.
    /// Return true on success.
    /// It can also publishes platform variants.
    ///
    /// # Arguments
    ///
    /// * `path` - Host's destination path (e.g., "./tarball").
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerExportOpts,
    ) -> Result<bool, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        if let Some(forced_compression) = opts.forced_compression {
            query = query.arg_enum("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg_enum("mediaTypes", media_types);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of exposed ports.
    /// This includes ports already exposed by the image, even if not
    /// explicitly added with dagger.
    pub fn exposed_ports(&self) -> Vec<Port> {
        let query = self.selection.select("exposedPorts");
        return vec![Port {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// Retrieves a file at the given path.
    /// Mounts are included.
    ///
    /// # Arguments
    ///
    /// * `path` - The path of the file to retrieve (e.g., "./README.md").
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// A unique identifier for this container.
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
    /// NOTE: this involves unpacking the tarball to an OCI store on the host at
    /// $XDG_CACHE_DIR/dagger/oci. This directory can be removed whenever you like.
    ///
    /// # Arguments
    ///
    /// * `source` - File to read the container from.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn import(&self, source: File) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Reads the container from an OCI tarball.
    /// NOTE: this involves unpacking the tarball to an OCI store on the host at
    /// $XDG_CACHE_DIR/dagger/oci. This directory can be removed whenever you like.
    ///
    /// # Arguments
    ///
    /// * `source` - File to read the container from.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn import_opts<'a>(&self, source: File, opts: ContainerImportOpts<'a>) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(tag) = opts.tag {
            query = query.arg("tag", tag);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves the value of the specified label.
    pub async fn label(&self, name: impl Into<String>) -> Result<String, DaggerError> {
        let mut query = self.selection.select("label");
        query = query.arg("name", name.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of labels passed to container.
    pub fn labels(&self) -> Vec<Label> {
        let query = self.selection.select("labels");
        return vec![Label {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// Retrieves the list of paths where a directory is mounted.
    pub async fn mounts(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("mounts");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a named sub-pipeline
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a named sub-pipeline
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: ContainerPipelineOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(labels) = opts.labels {
            query = query.arg("labels", labels);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
            query = query.arg_enum("forcedCompression", forced_compression);
        }
        if let Some(media_types) = opts.media_types {
            query = query.arg_enum("mediaTypes", media_types);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves this container's root filesystem. Mounts are not included.
    pub fn rootfs(&self) -> Directory {
        let query = self.selection.select("rootfs");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Return a websocket endpoint that, if connected to, will start the container with a TTY streamed
    /// over the websocket.
    /// Primarily intended for internal use with the dagger CLI.
    pub async fn shell_endpoint(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("shellEndpoint");
        query.execute(self.graphql_client.clone()).await
    }
    /// The error stream of the last executed command.
    /// Will execute default command if none is set, or error if there's no default.
    pub async fn stderr(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("stderr");
        query.execute(self.graphql_client.clone()).await
    }
    /// The output stream of the last executed command.
    /// Will execute default command if none is set, or error if there's no default.
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
    /// Retrieves the user to be set for all commands.
    pub async fn user(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("user");
        query.execute(self.graphql_client.clone()).await
    }
    /// Configures default arguments for future commands.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_args(&self) -> Container {
        let query = self.selection.select("withDefaultArgs");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Configures default arguments for future commands.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_args_opts<'a>(&self, opts: ContainerWithDefaultArgsOpts<'a>) -> Container {
        let mut query = self.selection.select("withDefaultArgs");
        if let Some(args) = opts.args {
            query = query.arg("args", args);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/tmp/directory").
    /// * `directory` - Identifier of the directory to write
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(&self, path: impl Into<String>, directory: Directory) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        directory: Directory,
        opts: ContainerWithDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.id().await.unwrap().quote() })
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container after executing the specified command inside it.
    ///
    /// # Arguments
    ///
    /// * `args` - Command to run instead of the container's default command (e.g., ["run", "main.go"]).
    ///
    /// If empty, the container's default command is used.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_exec(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withExec");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container after executing the specified command inside it.
    ///
    /// # Arguments
    ///
    /// * `args` - Command to run instead of the container's default command (e.g., ["run", "main.go"]).
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
        if let Some(skip_entrypoint) = opts.skip_entrypoint {
            query = query.arg("skipEntrypoint", skip_entrypoint);
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
        if let Some(experimental_privileged_nesting) = opts.experimental_privileged_nesting {
            query = query.arg(
                "experimentalPrivilegedNesting",
                experimental_privileged_nesting,
            );
        }
        if let Some(insecure_root_capabilities) = opts.insecure_root_capabilities {
            query = query.arg("insecureRootCapabilities", insecure_root_capabilities);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
            query = query.arg_enum("protocol", protocol);
        }
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: File) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: File,
        opts: ContainerWithFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Indicate that subsequent operations should be featured more prominently in
    /// the UI.
    pub fn with_focus(&self) -> Container {
        let query = self.selection.select("withFocus");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/cache/node_modules").
    /// * `cache` - Identifier of the cache volume to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache(&self, path: impl Into<String>, cache: CacheVolume) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "cache",
            Box::new(move || {
                let cache = cache.clone();
                Box::pin(async move { cache.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/cache/node_modules").
    /// * `cache` - Identifier of the cache volume to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache_opts<'a>(
        &self,
        path: impl Into<String>,
        cache: CacheVolume,
        opts: ContainerWithMountedCacheOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "cache",
            Box::new(move || {
                let cache = cache.clone();
                Box::pin(async move { cache.id().await.unwrap().quote() })
            }),
        );
        if let Some(source) = opts.source {
            query = query.arg("source", source);
        }
        if let Some(sharing) = opts.sharing {
            query = query.arg_enum("sharing", sharing);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a directory mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted directory (e.g., "/mnt/directory").
    /// * `source` - Identifier of the mounted directory.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_directory(&self, path: impl Into<String>, source: Directory) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: Directory,
        opts: ContainerWithMountedDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a file mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the mounted file (e.g., "/tmp/file.txt").
    /// * `source` - Identifier of the mounted file.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_file(&self, path: impl Into<String>, source: File) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: File,
        opts: ContainerWithMountedFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a secret mounted into a file at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the secret file (e.g., "/tmp/secret.txt").
    /// * `source` - Identifier of the secret to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_secret(&self, path: impl Into<String>, source: Secret) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: Secret,
        opts: ContainerWithMountedSecretOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        if let Some(mode) = opts.mode {
            query = query.arg("mode", mode);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a temporary directory mounted at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the temporary directory (e.g., "/tmp/temp_dir").
    pub fn with_mounted_temp(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withMountedTemp");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/tmp/file.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a new file written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written file (e.g., "/tmp/file.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file_opts<'a>(
        &self,
        path: impl Into<String>,
        opts: ContainerWithNewFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        if let Some(contents) = opts.contents {
            query = query.arg("contents", contents);
        }
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with a registry authentication for a given address.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to bind the authentication to.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    /// * `username` - The username of the registry's account (e.g., "Dagger").
    /// * `secret` - The API key, password or token to authenticate to this registry.
    pub fn with_registry_auth(
        &self,
        address: impl Into<String>,
        username: impl Into<String>,
        secret: Secret,
    ) -> Container {
        let mut query = self.selection.select("withRegistryAuth");
        query = query.arg("address", address.into());
        query = query.arg("username", username.into());
        query = query.arg_lazy(
            "secret",
            Box::new(move || {
                let secret = secret.clone();
                Box::pin(async move { secret.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Initializes this container from this DirectoryID.
    pub fn with_rootfs(&self, directory: Directory) -> Container {
        let mut query = self.selection.select("withRootfs");
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus an env variable containing the given secret.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the secret variable (e.g., "API_SECRET").
    /// * `secret` - The identifier of the secret value.
    pub fn with_secret_variable(&self, name: impl Into<String>, secret: Secret) -> Container {
        let mut query = self.selection.select("withSecretVariable");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "secret",
            Box::new(move || {
                let secret = secret.clone();
                Box::pin(async move { secret.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Establish a runtime dependency on a service.
    /// The service will be started automatically when needed and detached when it is
    /// no longer needed, executing the default command if none is set.
    /// The service will be reachable from the container via the provided hostname alias.
    /// The service dependency will also convey to any files or directories produced by the container.
    ///
    /// # Arguments
    ///
    /// * `alias` - A name that can be used to reach the service from the container
    /// * `service` - Identifier of the service container
    pub fn with_service_binding(&self, alias: impl Into<String>, service: Service) -> Container {
        let mut query = self.selection.select("withServiceBinding");
        query = query.arg("alias", alias.into());
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container plus a socket forwarded to the given Unix socket path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the forwarded Unix socket (e.g., "/tmp/socket").
    /// * `source` - Identifier of the socket to forward.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_unix_socket(&self, path: impl Into<String>, source: Socket) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: Socket,
        opts: ContainerWithUnixSocketOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with a different command user.
    ///
    /// # Arguments
    ///
    /// * `name` - The user to set (e.g., "root").
    pub fn with_user(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withUser");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with a different working directory.
    ///
    /// # Arguments
    ///
    /// * `path` - The path to set as the working directory (e.g., "/app").
    pub fn with_workdir(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withWorkdir");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with unset default arguments for future commands.
    pub fn without_default_args(&self) -> Container {
        let query = self.selection.select("withoutDefaultArgs");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with an unset command entrypoint.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn without_entrypoint(&self) -> Container {
        let query = self.selection.select("withoutEntrypoint");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container minus the given environment variable.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the environment variable (e.g., "HOST").
    pub fn without_env_variable(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutEnvVariable");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
            query = query.arg_enum("protocol", protocol);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Indicate that subsequent operations should not be featured more prominently
    /// in the UI.
    /// This is the initial state of all containers.
    pub fn without_focus(&self) -> Container {
        let query = self.selection.select("withoutFocus");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container minus the given environment label.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the label to remove (e.g., "org.opencontainers.artifact.created").
    pub fn without_label(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutLabel");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container after unmounting everything at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the cache directory (e.g., "/cache/node_modules").
    pub fn without_mount(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutMount");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container without the registry authentication of a given address.
    ///
    /// # Arguments
    ///
    /// * `address` - Registry's address to remove the authentication from.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    pub fn without_registry_auth(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutRegistryAuth");
        query = query.arg("address", address.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with a previously added Unix socket removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the socket to remove (e.g., "/tmp/socket").
    pub fn without_unix_socket(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with an unset command user.
    /// Should default to root.
    pub fn without_user(&self) -> Container {
        let query = self.selection.select("withoutUser");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this container with an unset working directory.
    /// Should default to "/".
    pub fn without_workdir(&self) -> Container {
        let query = self.selection.select("withoutWorkdir");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves the working directory for all commands.
    pub async fn workdir(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("workdir");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Directory {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryAsModuleOpts<'a> {
    /// An optional subpath of the directory which contains the module's source
    /// code.
    /// This is needed when the module code is in a subdirectory but requires
    /// parent directories to be loaded in order to execute. For example, the
    /// module source code may need a go.mod, project.toml, package.json, etc. file
    /// from a parent directory.
    /// If not set, the module source code is loaded from the root of the
    /// directory.
    #[builder(setter(into, strip_option), default)]
    pub source_subpath: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryDockerBuildOpts<'a> {
    /// Build arguments to use in the build.
    #[builder(setter(into, strip_option), default)]
    pub build_args: Option<Vec<BuildArg>>,
    /// Path to the Dockerfile to use (e.g., "frontend.Dockerfile").
    /// Defaults: './Dockerfile'.
    #[builder(setter(into, strip_option), default)]
    pub dockerfile: Option<&'a str>,
    /// The platform to build.
    #[builder(setter(into, strip_option), default)]
    pub platform: Option<Platform>,
    /// Secrets to pass to the build.
    /// They will be mounted at /run/secrets/[secret-name].
    #[builder(setter(into, strip_option), default)]
    pub secrets: Option<Vec<SecretId>>,
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
pub struct DirectoryPipelineOpts<'a> {
    /// Pipeline description.
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Pipeline labels.
    #[builder(setter(into, strip_option), default)]
    pub labels: Option<Vec<PipelineLabel>>,
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
    /// Default: 0644.
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewDirectoryOpts {
    /// Permission granted to the created directory (e.g., 0777).
    /// Default: 0755.
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewFileOpts {
    /// Permission given to the copied file (e.g., 0600).
    /// Default: 0644.
    #[builder(setter(into, strip_option), default)]
    pub permissions: Option<isize>,
}
impl Directory {
    /// Load the directory as a Dagger module
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module(&self) -> Module {
        let query = self.selection.select("asModule");
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load the directory as a Dagger module
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn as_module_opts<'a>(&self, opts: DirectoryAsModuleOpts<'a>) -> Module {
        let mut query = self.selection.select("asModule");
        if let Some(source_subpath) = opts.source_subpath {
            query = query.arg("sourceSubpath", source_subpath);
        }
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Gets the difference between this directory and an another directory.
    ///
    /// # Arguments
    ///
    /// * `other` - Identifier of the directory to compare.
    pub fn diff(&self, other: Directory) -> Directory {
        let mut query = self.selection.select("diff");
        query = query.arg_lazy(
            "other",
            Box::new(move || {
                let other = other.clone();
                Box::pin(async move { other.id().await.unwrap().quote() })
            }),
        );
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves a directory at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to retrieve (e.g., "/src").
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Builds a new Docker container from this directory.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn docker_build(&self) -> Container {
        let query = self.selection.select("dockerBuild");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Builds a new Docker container from this directory.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn docker_build_opts<'a>(&self, opts: DirectoryDockerBuildOpts<'a>) -> Container {
        let mut query = self.selection.select("dockerBuild");
        if let Some(dockerfile) = opts.dockerfile {
            query = query.arg("dockerfile", dockerfile);
        }
        if let Some(platform) = opts.platform {
            query = query.arg("platform", platform);
        }
        if let Some(build_args) = opts.build_args {
            query = query.arg("buildArgs", build_args);
        }
        if let Some(target) = opts.target {
            query = query.arg("target", target);
        }
        if let Some(secrets) = opts.secrets {
            query = query.arg("secrets", secrets);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
    pub async fn export(&self, path: impl Into<String>) -> Result<bool, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
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
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
    /// The content-addressed identifier of the directory.
    pub async fn id(&self) -> Result<DirectoryId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a named sub-pipeline
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Directory {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a named sub-pipeline
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline_opts<'a>(
        &self,
        name: impl Into<String>,
        opts: DirectoryPipelineOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(labels) = opts.labels {
            query = query.arg("labels", labels);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Force evaluation in the engine.
    pub async fn sync(&self) -> Result<DirectoryId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves this directory plus a directory written at the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "/src/").
    /// * `directory` - Identifier of the directory to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(&self, path: impl Into<String>, directory: Directory) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.id().await.unwrap().quote() })
            }),
        );
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        directory: Directory,
        opts: DirectoryWithDirectoryOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "directory",
            Box::new(move || {
                let directory = directory.clone();
                Box::pin(async move { directory.id().await.unwrap().quote() })
            }),
        );
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the copied file (e.g., "/file.txt").
    /// * `source` - Identifier of the file to copy.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: File) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        source: File,
        opts: DirectoryWithFileOpts,
    ) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg_lazy(
            "source",
            Box::new(move || {
                let source = source.clone();
                Box::pin(async move { source.id().await.unwrap().quote() })
            }),
        );
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this directory with the directory at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the directory to remove (e.g., ".github/").
    pub fn without_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutDirectory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves this directory with the file at the given path removed.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to remove (e.g., "/file.txt").
    pub fn without_file(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutFile");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct EnvVariable {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl EnvVariable {
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
pub struct FieldTypeDef {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FieldTypeDef {
    /// A doc string for the field, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the field in the object
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The type of the field
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct File {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct FileExportOpts {
    /// If allowParentDirPath is true, the path argument can be a directory path, in which case
    /// the file will be created in that directory.
    #[builder(setter(into, strip_option), default)]
    pub allow_parent_dir_path: Option<bool>,
}
impl File {
    /// Retrieves the contents of the file.
    pub async fn contents(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("contents");
        query.execute(self.graphql_client.clone()).await
    }
    /// Writes the file to a file path on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the written directory (e.g., "output.txt").
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> Result<bool, DaggerError> {
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
    ) -> Result<bool, DaggerError> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(allow_parent_dir_path) = opts.allow_parent_dir_path {
            query = query.arg("allowParentDirPath", allow_parent_dir_path);
        }
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the content-addressed identifier of the file.
    pub async fn id(&self) -> Result<FileId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Gets the size of the file, in bytes.
    pub async fn size(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("size");
        query.execute(self.graphql_client.clone()).await
    }
    /// Force evaluation in the engine.
    pub async fn sync(&self) -> Result<FileId, DaggerError> {
        let query = self.selection.select("sync");
        query.execute(self.graphql_client.clone()).await
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
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct Function {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct FunctionWithArgOpts<'a> {
    /// A default value to use for this argument if not explicitly set by the caller, if any
    #[builder(setter(into, strip_option), default)]
    pub default_value: Option<Json>,
    /// A doc string for the argument, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
}
impl Function {
    /// Arguments accepted by this function, if any
    pub fn args(&self) -> Vec<FunctionArg> {
        let query = self.selection.select("args");
        return vec![FunctionArg {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// A doc string for the function, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The ID of the function
    pub async fn id(&self) -> Result<FunctionId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the function
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The type returned by this function
    pub fn return_type(&self) -> TypeDef {
        let query = self.selection.select("returnType");
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns the function with the provided argument
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the argument
    /// * `type_def` - The type of the argument
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_arg(&self, name: impl Into<String>, type_def: TypeDef) -> Function {
        let mut query = self.selection.select("withArg");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.id().await.unwrap().quote() })
            }),
        );
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        type_def: TypeDef,
        opts: FunctionWithArgOpts<'a>,
    ) -> Function {
        let mut query = self.selection.select("withArg");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.id().await.unwrap().quote() })
            }),
        );
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(default_value) = opts.default_value {
            query = query.arg("defaultValue", default_value);
        }
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns the function with the doc string
    pub fn with_description(&self, description: impl Into<String>) -> Function {
        let mut query = self.selection.select("withDescription");
        query = query.arg("description", description.into());
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct FunctionArg {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionArg {
    /// A default value to use for this argument when not explicitly set by the caller, if any
    pub async fn default_value(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("defaultValue");
        query.execute(self.graphql_client.clone()).await
    }
    /// A doc string for the argument, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The ID of the argument
    pub async fn id(&self) -> Result<FunctionArgId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the argument
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The type of the argument
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct FunctionCall {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionCall {
    /// The argument values the function is being invoked with.
    pub fn input_args(&self) -> Vec<FunctionCallArgValue> {
        let query = self.selection.select("inputArgs");
        return vec![FunctionCallArgValue {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// The name of the function being called.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of the parent object of the function being called.
    /// If the function is "top-level" to the module, this is always an empty object.
    pub async fn parent(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("parent");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the parent object of the function being called.
    /// If the function is "top-level" to the module, this is the name of the module.
    pub async fn parent_name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("parentName");
        query.execute(self.graphql_client.clone()).await
    }
    /// Set the return value of the function call to the provided value.
    /// The value should be a string of the JSON serialization of the return value.
    pub async fn return_value(&self, value: Json) -> Result<Void, DaggerError> {
        let mut query = self.selection.select("returnValue");
        query = query.arg("value", value);
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct FunctionCallArgValue {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl FunctionCallArgValue {
    /// The name of the argument.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of the argument represented as a string of the JSON serialization.
    pub async fn value(&self) -> Result<Json, DaggerError> {
        let query = self.selection.select("value");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct GeneratedCode {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl GeneratedCode {
    /// The directory containing the generated code
    pub fn code(&self) -> Directory {
        let query = self.selection.select("code");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    pub async fn id(&self) -> Result<GeneratedCodeId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// List of paths to mark generated in version control (i.e. .gitattributes)
    pub async fn vcs_generated_paths(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("vcsGeneratedPaths");
        query.execute(self.graphql_client.clone()).await
    }
    /// List of paths to ignore in version control (i.e. .gitignore)
    pub async fn vcs_ignored_paths(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("vcsIgnoredPaths");
        query.execute(self.graphql_client.clone()).await
    }
    /// Set the list of paths to mark generated in version control
    pub fn with_vcs_generated_paths(&self, paths: Vec<impl Into<String>>) -> GeneratedCode {
        let mut query = self.selection.select("withVCSGeneratedPaths");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        return GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Set the list of paths to ignore in version control
    pub fn with_vcs_ignored_paths(&self, paths: Vec<impl Into<String>>) -> GeneratedCode {
        let mut query = self.selection.select("withVCSIgnoredPaths");
        query = query.arg(
            "paths",
            paths.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        return GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct GitRef {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct GitRefTreeOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub ssh_auth_socket: Option<SocketId>,
    #[builder(setter(into, strip_option), default)]
    pub ssh_known_hosts: Option<&'a str>,
}
impl GitRef {
    /// The resolved commit id at this ref.
    pub async fn commit(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("commit");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the content-addressed identifier of the git ref.
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The filesystem tree at this ref.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tree_opts<'a>(&self, opts: GitRefTreeOpts<'a>) -> Directory {
        let mut query = self.selection.select("tree");
        if let Some(ssh_known_hosts) = opts.ssh_known_hosts {
            query = query.arg("sshKnownHosts", ssh_known_hosts);
        }
        if let Some(ssh_auth_socket) = opts.ssh_auth_socket {
            query = query.arg("sshAuthSocket", ssh_auth_socket);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct GitRepository {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl GitRepository {
    /// Returns details on one branch.
    ///
    /// # Arguments
    ///
    /// * `name` - Branch's name (e.g., "main").
    pub fn branch(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("branch");
        query = query.arg("name", name.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns details on one commit.
    ///
    /// # Arguments
    ///
    /// * `id` - Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").
    pub fn commit(&self, id: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("commit");
        query = query.arg("id", id.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Retrieves the content-addressed identifier of the git repository.
    pub async fn id(&self) -> Result<GitRepositoryId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Returns details on one tag.
    ///
    /// # Arguments
    ///
    /// * `name` - Tag's name (e.g., "v0.3.9").
    pub fn tag(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("tag");
        query = query.arg("name", name.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct Host {
    pub proc: Option<Arc<Child>>,
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
    /// Map each service port to the same port on the host, as if the service were
    /// running natively.
    /// Note: enabling may result in port conflicts.
    #[builder(setter(into, strip_option), default)]
    pub native: Option<bool>,
    /// Configure explicit port forwarding rules for the tunnel.
    /// If a port's frontend is unspecified or 0, a random port will be chosen by
    /// the host.
    /// If no ports are given, all of the service's ports are forwarded. If native
    /// is true, each port maps to the same port on the host. If native is false,
    /// each port maps to a random port chosen by the host.
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Accesses a file on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the file to retrieve (e.g., "README.md").
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a service that forwards traffic to a specified address via the host.
    ///
    /// # Arguments
    ///
    /// * `ports` - Ports to expose via the service, forwarding through the host network.
    ///
    /// If a port's frontend is unspecified or 0, it defaults to the same as the
    /// backend port.
    ///
    /// An empty set of ports is not valid; an error will be returned.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn service(&self, ports: Vec<PortForward>) -> Service {
        let mut query = self.selection.select("service");
        query = query.arg("ports", ports);
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a service that forwards traffic to a specified address via the host.
    ///
    /// # Arguments
    ///
    /// * `ports` - Ports to expose via the service, forwarding through the host network.
    ///
    /// If a port's frontend is unspecified or 0, it defaults to the same as the
    /// backend port.
    ///
    /// An empty set of ports is not valid; an error will be returned.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn service_opts<'a>(&self, ports: Vec<PortForward>, opts: HostServiceOpts<'a>) -> Service {
        let mut query = self.selection.select("service");
        query = query.arg("ports", ports);
        if let Some(host) = opts.host {
            query = query.arg("host", host);
        }
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a tunnel that forwards traffic from the host to a service.
    ///
    /// # Arguments
    ///
    /// * `service` - Service to send traffic from the tunnel.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tunnel(&self, service: Service) -> Service {
        let mut query = self.selection.select("tunnel");
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.id().await.unwrap().quote() })
            }),
        );
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a tunnel that forwards traffic from the host to a service.
    ///
    /// # Arguments
    ///
    /// * `service` - Service to send traffic from the tunnel.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tunnel_opts(&self, service: Service, opts: HostTunnelOpts) -> Service {
        let mut query = self.selection.select("tunnel");
        query = query.arg_lazy(
            "service",
            Box::new(move || {
                let service = service.clone();
                Box::pin(async move { service.id().await.unwrap().quote() })
            }),
        );
        if let Some(native) = opts.native {
            query = query.arg("native", native);
        }
        if let Some(ports) = opts.ports {
            query = query.arg("ports", ports);
        }
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Accesses a Unix socket on the host.
    ///
    /// # Arguments
    ///
    /// * `path` - Location of the Unix socket (e.g., "/var/run/docker.sock").
    pub fn unix_socket(&self, path: impl Into<String>) -> Socket {
        let mut query = self.selection.select("unixSocket");
        query = query.arg("path", path.into());
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct Label {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Label {
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
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ListTypeDef {
    /// The type of the elements in the list
    pub fn element_type_def(&self) -> TypeDef {
        let query = self.selection.select("elementTypeDef");
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct Module {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Module {
    /// Modules used by this module
    pub fn dependencies(&self) -> Vec<Module> {
        let query = self.selection.select("dependencies");
        return vec![Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// The dependencies as configured by the module
    pub async fn dependency_config(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("dependencyConfig");
        query.execute(self.graphql_client.clone()).await
    }
    /// The doc string of the module, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The code generated by the SDK's runtime
    pub fn generated_code(&self) -> GeneratedCode {
        let query = self.selection.select("generatedCode");
        return GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The ID of the module
    pub async fn id(&self) -> Result<ModuleId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the module
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// Objects served by this module
    pub fn objects(&self) -> Vec<TypeDef> {
        let query = self.selection.select("objects");
        return vec![TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// The SDK used by this module. Either a name of a builtin SDK or a module ref pointing to the SDK's implementation.
    pub async fn sdk(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sdk");
        query.execute(self.graphql_client.clone()).await
    }
    /// Serve a module's API in the current session.
    /// Note: this can only be called once per session.
    /// In the future, it could return a stream or service to remove the side effect.
    pub async fn serve(&self) -> Result<Void, DaggerError> {
        let query = self.selection.select("serve");
        query.execute(self.graphql_client.clone()).await
    }
    /// The directory containing the module's source code
    pub fn source_directory(&self) -> Directory {
        let query = self.selection.select("sourceDirectory");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The module's subpath within the source directory
    pub async fn source_directory_sub_path(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sourceDirectorySubPath");
        query.execute(self.graphql_client.clone()).await
    }
    /// This module plus the given Object type and associated functions
    pub fn with_object(&self, object: TypeDef) -> Module {
        let mut query = self.selection.select("withObject");
        query = query.arg_lazy(
            "object",
            Box::new(move || {
                let object = object.clone();
                Box::pin(async move { object.id().await.unwrap().quote() })
            }),
        );
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct ModuleConfig {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ModuleConfig {
    /// Modules that this module depends on.
    pub async fn dependencies(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("dependencies");
        query.execute(self.graphql_client.clone()).await
    }
    /// Exclude these file globs when loading the module root.
    pub async fn exclude(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("exclude");
        query.execute(self.graphql_client.clone()).await
    }
    /// Include only these file globs when loading the module root.
    pub async fn include(&self) -> Result<Vec<String>, DaggerError> {
        let query = self.selection.select("include");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the module.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The root directory of the module's project, which may be above the module source code.
    pub async fn root(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("root");
        query.execute(self.graphql_client.clone()).await
    }
    /// Either the name of a built-in SDK ('go', 'python', etc.) OR a module reference pointing to the SDK's module implementation.
    pub async fn sdk(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("sdk");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct ObjectTypeDef {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ObjectTypeDef {
    /// The function used to construct new instances of this object, if any
    pub fn constructor(&self) -> Function {
        let query = self.selection.select("constructor");
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The doc string for the object, if any
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Static fields defined on this object, if any
    pub fn fields(&self) -> Vec<FieldTypeDef> {
        let query = self.selection.select("fields");
        return vec![FieldTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// Functions defined on this object, if any
    pub fn functions(&self) -> Vec<Function> {
        let query = self.selection.select("functions");
        return vec![Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// The name of the object
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Port {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Port {
    /// The port description.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The port number.
    pub async fn port(&self) -> Result<isize, DaggerError> {
        let query = self.selection.select("port");
        query.execute(self.graphql_client.clone()).await
    }
    /// The transport layer network protocol.
    pub async fn protocol(&self) -> Result<NetworkProtocol, DaggerError> {
        let query = self.selection.select("protocol");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Query {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryContainerOpts {
    #[builder(setter(into, strip_option), default)]
    pub id: Option<ContainerId>,
    #[builder(setter(into, strip_option), default)]
    pub platform: Option<Platform>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryDirectoryOpts {
    #[builder(setter(into, strip_option), default)]
    pub id: Option<DirectoryId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryGitOpts<'a> {
    /// A service which must be started before the repo is fetched.
    #[builder(setter(into, strip_option), default)]
    pub experimental_service_host: Option<ServiceId>,
    /// Set to true to keep .git directory.
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
pub struct QueryModuleConfigOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub subpath: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryPipelineOpts<'a> {
    /// Pipeline description.
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
    /// Pipeline labels.
    #[builder(setter(into, strip_option), default)]
    pub labels: Option<Vec<PipelineLabel>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QuerySocketOpts {
    #[builder(setter(into, strip_option), default)]
    pub id: Option<SocketId>,
}
impl Query {
    /// Constructs a cache volume for a given cache key.
    ///
    /// # Arguments
    ///
    /// * `key` - A string identifier to target this cache volume (e.g., "modules-cache").
    pub fn cache_volume(&self, key: impl Into<String>) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");
        query = query.arg("key", key.into());
        return CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Checks if the current Dagger Engine is compatible with an SDK's required version.
    ///
    /// # Arguments
    ///
    /// * `version` - The SDK's required version.
    pub async fn check_version_compatibility(
        &self,
        version: impl Into<String>,
    ) -> Result<bool, DaggerError> {
        let mut query = self.selection.select("checkVersionCompatibility");
        query = query.arg("version", version.into());
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates a scratch container or loads one by ID.
    /// Optional platform argument initializes new containers to execute and publish
    /// as that platform. Platform defaults to that of the builder's host.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn container(&self) -> Container {
        let query = self.selection.select("container");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a scratch container or loads one by ID.
    /// Optional platform argument initializes new containers to execute and publish
    /// as that platform. Platform defaults to that of the builder's host.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn container_opts(&self, opts: QueryContainerOpts) -> Container {
        let mut query = self.selection.select("container");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        if let Some(platform) = opts.platform {
            query = query.arg("platform", platform);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The FunctionCall context that the SDK caller is currently executing in.
    /// If the caller is not currently executing in a function, this will return
    /// an error.
    pub fn current_function_call(&self) -> FunctionCall {
        let query = self.selection.select("currentFunctionCall");
        return FunctionCall {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The module currently being served in the session, if any.
    pub fn current_module(&self) -> Module {
        let query = self.selection.select("currentModule");
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// The default platform of the builder.
    pub async fn default_platform(&self) -> Result<Platform, DaggerError> {
        let query = self.selection.select("defaultPlatform");
        query.execute(self.graphql_client.clone()).await
    }
    /// Creates an empty directory or loads one by ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory(&self) -> Directory {
        let query = self.selection.select("directory");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates an empty directory or loads one by ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory_opts(&self, opts: QueryDirectoryOpts) -> Directory {
        let mut query = self.selection.select("directory");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a file by ID.
    pub fn file(&self, id: File) -> File {
        let mut query = self.selection.select("file");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Create a function.
    pub fn function(&self, name: impl Into<String>, return_type: TypeDef) -> Function {
        let mut query = self.selection.select("function");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "returnType",
            Box::new(move || {
                let return_type = return_type.clone();
                Box::pin(async move { return_type.id().await.unwrap().quote() })
            }),
        );
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Create a code generation result, given a directory containing the generated
    /// code.
    pub fn generated_code(&self, code: Directory) -> GeneratedCode {
        let mut query = self.selection.select("generatedCode");
        query = query.arg_lazy(
            "code",
            Box::new(move || {
                let code = code.clone();
                Box::pin(async move { code.id().await.unwrap().quote() })
            }),
        );
        return GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Queries a git repository.
    ///
    /// # Arguments
    ///
    /// * `url` - Url of the git repository.
    /// Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`
    /// Suffix ".git" is optional.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git(&self, url: impl Into<String>) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Queries a git repository.
    ///
    /// # Arguments
    ///
    /// * `url` - Url of the git repository.
    /// Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`
    /// Suffix ".git" is optional.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git_opts<'a>(&self, url: impl Into<String>, opts: QueryGitOpts<'a>) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        if let Some(keep_git_dir) = opts.keep_git_dir {
            query = query.arg("keepGitDir", keep_git_dir);
        }
        if let Some(ssh_known_hosts) = opts.ssh_known_hosts {
            query = query.arg("sshKnownHosts", ssh_known_hosts);
        }
        if let Some(ssh_auth_socket) = opts.ssh_auth_socket {
            query = query.arg("sshAuthSocket", ssh_auth_socket);
        }
        if let Some(experimental_service_host) = opts.experimental_service_host {
            query = query.arg("experimentalServiceHost", experimental_service_host);
        }
        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Queries the host environment.
    pub fn host(&self) -> Host {
        let query = self.selection.select("host");
        return Host {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a CacheVolume from its ID.
    pub fn load_cache_volume_from_id(&self, id: CacheVolume) -> CacheVolume {
        let mut query = self.selection.select("loadCacheVolumeFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a container from an ID.
    pub fn load_container_from_id(&self, id: Container) -> Container {
        let mut query = self.selection.select("loadContainerFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a Directory from its ID.
    pub fn load_directory_from_id(&self, id: Directory) -> Directory {
        let mut query = self.selection.select("loadDirectoryFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a File from its ID.
    pub fn load_file_from_id(&self, id: File) -> File {
        let mut query = self.selection.select("loadFileFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return File {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a function argument by ID.
    pub fn load_function_arg_from_id(&self, id: FunctionArg) -> FunctionArg {
        let mut query = self.selection.select("loadFunctionArgFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return FunctionArg {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a function by ID.
    pub fn load_function_from_id(&self, id: Function) -> Function {
        let mut query = self.selection.select("loadFunctionFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Function {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a GeneratedCode by ID.
    pub fn load_generated_code_from_id(&self, id: GeneratedCode) -> GeneratedCode {
        let mut query = self.selection.select("loadGeneratedCodeFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return GeneratedCode {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a git ref from its ID.
    pub fn load_git_ref_from_id(&self, id: GitRef) -> GitRef {
        let mut query = self.selection.select("loadGitRefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a git repository from its ID.
    pub fn load_git_repository_from_id(&self, id: GitRepository) -> GitRepository {
        let mut query = self.selection.select("loadGitRepositoryFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a module by ID.
    pub fn load_module_from_id(&self, id: Module) -> Module {
        let mut query = self.selection.select("loadModuleFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a Secret from its ID.
    pub fn load_secret_from_id(&self, id: Secret) -> Secret {
        let mut query = self.selection.select("loadSecretFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a service from ID.
    pub fn load_service_from_id(&self, id: Service) -> Service {
        let mut query = self.selection.select("loadServiceFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Service {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a Socket from its ID.
    pub fn load_socket_from_id(&self, id: Socket) -> Socket {
        let mut query = self.selection.select("loadSocketFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a TypeDef by ID.
    pub fn load_type_def_from_id(&self, id: TypeDef) -> TypeDef {
        let mut query = self.selection.select("loadTypeDefFromID");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Create a new module.
    pub fn module(&self) -> Module {
        let query = self.selection.select("module");
        return Module {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load the static configuration for a module from the given source directory and optional subpath.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn module_config(&self, source_directory: Directory) -> ModuleConfig {
        let mut query = self.selection.select("moduleConfig");
        query = query.arg_lazy(
            "sourceDirectory",
            Box::new(move || {
                let source_directory = source_directory.clone();
                Box::pin(async move { source_directory.id().await.unwrap().quote() })
            }),
        );
        return ModuleConfig {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load the static configuration for a module from the given source directory and optional subpath.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn module_config_opts<'a>(
        &self,
        source_directory: Directory,
        opts: QueryModuleConfigOpts<'a>,
    ) -> ModuleConfig {
        let mut query = self.selection.select("moduleConfig");
        query = query.arg_lazy(
            "sourceDirectory",
            Box::new(move || {
                let source_directory = source_directory.clone();
                Box::pin(async move { source_directory.id().await.unwrap().quote() })
            }),
        );
        if let Some(subpath) = opts.subpath {
            query = query.arg("subpath", subpath);
        }
        return ModuleConfig {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a named sub-pipeline.
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Query {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Query {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Creates a named sub-pipeline.
    ///
    /// # Arguments
    ///
    /// * `name` - Pipeline name.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline_opts<'a>(&self, name: impl Into<String>, opts: QueryPipelineOpts<'a>) -> Query {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        if let Some(labels) = opts.labels {
            query = query.arg("labels", labels);
        }
        return Query {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a secret from its ID.
    pub fn secret(&self, id: Secret) -> Secret {
        let mut query = self.selection.select("secret");
        query = query.arg_lazy(
            "id",
            Box::new(move || {
                let id = id.clone();
                Box::pin(async move { id.id().await.unwrap().quote() })
            }),
        );
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a socket by its ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn socket(&self) -> Socket {
        let query = self.selection.select("socket");
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a socket by its ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn socket_opts(&self, opts: QuerySocketOpts) -> Socket {
        let mut query = self.selection.select("socket");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Create a new TypeDef.
    pub fn type_def(&self) -> TypeDef {
        let query = self.selection.select("typeDef");
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Clone)]
pub struct Secret {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Secret {
    /// The identifier for this secret.
    pub async fn id(&self) -> Result<SecretId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The value of this secret.
    pub async fn plaintext(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("plaintext");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Service {
    pub proc: Option<Arc<Child>>,
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
    /// A unique identifier for this service.
    pub async fn id(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Retrieves the list of ports provided by the service.
    pub fn ports(&self) -> Vec<Port> {
        let query = self.selection.select("ports");
        return vec![Port {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// Start the service and wait for its health checks to succeed.
    /// Services bound to a Container do not need to be manually started.
    pub async fn start(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("start");
        query.execute(self.graphql_client.clone()).await
    }
    /// Stop the service.
    pub async fn stop(&self) -> Result<ServiceId, DaggerError> {
        let query = self.selection.select("stop");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct Socket {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Socket {
    /// The content-addressed identifier of the socket.
    pub async fn id(&self) -> Result<SocketId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct TypeDef {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithFieldOpts<'a> {
    /// A doc string for the field, if any
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct TypeDefWithObjectOpts<'a> {
    #[builder(setter(into, strip_option), default)]
    pub description: Option<&'a str>,
}
impl TypeDef {
    /// If kind is LIST, the list-specific type definition.
    /// If kind is not LIST, this will be null.
    pub fn as_list(&self) -> ListTypeDef {
        let query = self.selection.select("asList");
        return ListTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// If kind is OBJECT, the object-specific type definition.
    /// If kind is not OBJECT, this will be null.
    pub fn as_object(&self) -> ObjectTypeDef {
        let query = self.selection.select("asObject");
        return ObjectTypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    pub async fn id(&self) -> Result<TypeDefId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The kind of type this is (e.g. primitive, list, object)
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
    pub fn with_constructor(&self, function: Function) -> TypeDef {
        let mut query = self.selection.select("withConstructor");
        query = query.arg_lazy(
            "function",
            Box::new(move || {
                let function = function.clone();
                Box::pin(async move { function.id().await.unwrap().quote() })
            }),
        );
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Adds a static field for an Object TypeDef, failing if the type is not an object.
    ///
    /// # Arguments
    ///
    /// * `name` - The name of the field in the object
    /// * `type_def` - The type of the field
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_field(&self, name: impl Into<String>, type_def: TypeDef) -> TypeDef {
        let mut query = self.selection.select("withField");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.id().await.unwrap().quote() })
            }),
        );
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
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
        type_def: TypeDef,
        opts: TypeDefWithFieldOpts<'a>,
    ) -> TypeDef {
        let mut query = self.selection.select("withField");
        query = query.arg("name", name.into());
        query = query.arg_lazy(
            "typeDef",
            Box::new(move || {
                let type_def = type_def.clone();
                Box::pin(async move { type_def.id().await.unwrap().quote() })
            }),
        );
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Adds a function for an Object TypeDef, failing if the type is not an object.
    pub fn with_function(&self, function: Function) -> TypeDef {
        let mut query = self.selection.select("withFunction");
        query = query.arg_lazy(
            "function",
            Box::new(move || {
                let function = function.clone();
                Box::pin(async move { function.id().await.unwrap().quote() })
            }),
        );
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Sets the kind of the type.
    pub fn with_kind(&self, kind: TypeDefKind) -> TypeDef {
        let mut query = self.selection.select("withKind");
        query = query.arg_enum("kind", kind);
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns a TypeDef of kind List with the provided type for its elements.
    pub fn with_list_of(&self, element_type: TypeDef) -> TypeDef {
        let mut query = self.selection.select("withListOf");
        query = query.arg_lazy(
            "elementType",
            Box::new(move || {
                let element_type = element_type.clone();
                Box::pin(async move { element_type.id().await.unwrap().quote() })
            }),
        );
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns a TypeDef of kind Object with the provided name.
    /// Note that an object's fields and functions may be omitted if the intent is
    /// only to refer to an object. This is how functions are able to return their
    /// own object, or any other circular reference.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_object(&self, name: impl Into<String>) -> TypeDef {
        let mut query = self.selection.select("withObject");
        query = query.arg("name", name.into());
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Returns a TypeDef of kind Object with the provided name.
    /// Note that an object's fields and functions may be omitted if the intent is
    /// only to refer to an object. This is how functions are able to return their
    /// own object, or any other circular reference.
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
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Sets whether this type can be set to null.
    pub fn with_optional(&self, optional: bool) -> TypeDef {
        let mut query = self.selection.select("withOptional");
        query = query.arg("optional", optional);
        return TypeDef {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum CacheSharingMode {
    LOCKED,
    PRIVATE,
    SHARED,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ImageLayerCompression {
    EStarGZ,
    Gzip,
    Uncompressed,
    Zstd,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum ImageMediaTypes {
    DockerMediaTypes,
    OCIMediaTypes,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum NetworkProtocol {
    TCP,
    UDP,
}
#[derive(Serialize, Deserialize, Clone, PartialEq, Debug)]
pub enum TypeDefKind {
    BooleanKind,
    IntegerKind,
    ListKind,
    ObjectKind,
    StringKind,
    VoidKind,
}
