use crate::core::graphql_client::DynGraphQLClient;
use crate::errors::DaggerError;
use crate::querybuilder::Selection;
use derive_builder::Builder;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tokio::process::Child;

#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct CacheId(pub String);
impl Into<CacheId> for &str {
    fn into(self) -> CacheId {
        CacheId(self.to_string())
    }
}
impl Into<CacheId> for String {
    fn into(self) -> CacheId {
        CacheId(self.clone())
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
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ProjectCommandId(pub String);
impl Into<ProjectCommandId> for &str {
    fn into(self) -> ProjectCommandId {
        ProjectCommandId(self.to_string())
    }
}
impl Into<ProjectCommandId> for String {
    fn into(self) -> ProjectCommandId {
        ProjectCommandId(self.clone())
    }
}
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ProjectId(pub String);
impl Into<ProjectId> for &str {
    fn into(self) -> ProjectId {
        ProjectId(self.to_string())
    }
}
impl Into<ProjectId> for String {
    fn into(self) -> ProjectId {
        ProjectId(self.clone())
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
#[derive(Clone)]
pub struct CacheVolume {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl CacheVolume {
    pub async fn id(&self) -> Result<CacheId, DaggerError> {
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
pub struct ContainerEndpointOpts<'a> {
    /// The exposed port number for the endpoint
    #[builder(setter(into, strip_option), default)]
    pub port: Option<isize>,
    /// Return a URL with the given scheme, eg. http for http://
    #[builder(setter(into, strip_option), default)]
    pub scheme: Option<&'a str>,
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
pub struct ContainerWithEnvVariableOpts {
    /// Replace ${VAR} or $VAR in the value according to the current environment
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
pub struct ContainerWithoutExposedPortOpts {
    /// Port protocol to unexpose
    #[builder(setter(into, strip_option), default)]
    pub protocol: Option<NetworkProtocol>,
}
impl Container {
    /// Initializes this container from a Dockerfile build.
    ///
    /// # Arguments
    ///
    /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build(&self, context: DirectoryId) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg("context", context);
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
    pub fn build_opts<'a>(&self, context: DirectoryId, opts: ContainerBuildOpts<'a>) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg("context", context);
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
    /// Retrieves an endpoint that clients can use to reach this container.
    /// If no port is specified, the first exposed port is used. If none exist an error is returned.
    /// If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn endpoint_opts<'a>(
        &self,
        opts: ContainerEndpointOpts<'a>,
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    /// Retrieves a hostname which can be used by clients to reach this container.
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
    pub async fn hostname(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("hostname");
        query.execute(self.graphql_client.clone()).await
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
    pub fn import(&self, source: FileId) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg("source", source);
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
    pub fn import_opts<'a>(&self, source: FileId, opts: ContainerImportOpts<'a>) -> Container {
        let mut query = self.selection.select("import");
        query = query.arg("source", source);
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
    pub fn with_directory(&self, path: impl Into<String>, directory: DirectoryId) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
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
        directory: DirectoryId,
        opts: ContainerWithDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    pub fn with_file(&self, path: impl Into<String>, source: FileId) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: FileId,
        opts: ContainerWithFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
    pub fn with_mounted_cache(&self, path: impl Into<String>, cache: CacheId) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg("cache", cache);
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
        cache: CacheId,
        opts: ContainerWithMountedCacheOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg("cache", cache);
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
    pub fn with_mounted_directory(
        &self,
        path: impl Into<String>,
        source: DirectoryId,
    ) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: DirectoryId,
        opts: ContainerWithMountedDirectoryOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedDirectory");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
    pub fn with_mounted_file(&self, path: impl Into<String>, source: FileId) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: FileId,
        opts: ContainerWithMountedFileOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
    pub fn with_mounted_secret(&self, path: impl Into<String>, source: SecretId) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: SecretId,
        opts: ContainerWithMountedSecretOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        if let Some(owner) = opts.owner {
            query = query.arg("owner", owner);
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
        secret: SecretId,
    ) -> Container {
        let mut query = self.selection.select("withRegistryAuth");
        query = query.arg("address", address.into());
        query = query.arg("username", username.into());
        query = query.arg("secret", secret);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Initializes this container from this DirectoryID.
    pub fn with_rootfs(&self, directory: DirectoryId) -> Container {
        let mut query = self.selection.select("withRootfs");
        query = query.arg("directory", directory);
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
    pub fn with_secret_variable(&self, name: impl Into<String>, secret: SecretId) -> Container {
        let mut query = self.selection.select("withSecretVariable");
        query = query.arg("name", name.into());
        query = query.arg("secret", secret);
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
    ///
    /// # Arguments
    ///
    /// * `alias` - A name that can be used to reach the service from the container
    /// * `service` - Identifier of the service container
    pub fn with_service_binding(
        &self,
        alias: impl Into<String>,
        service: ContainerId,
    ) -> Container {
        let mut query = self.selection.select("withServiceBinding");
        query = query.arg("alias", alias.into());
        query = query.arg("service", service);
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
    pub fn with_unix_socket(&self, path: impl Into<String>, source: SocketId) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: SocketId,
        opts: ContainerWithUnixSocketOpts<'a>,
    ) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    /// Currently experimental; set _EXPERIMENTAL_DAGGER_SERVICES_DNS=0 to disable.
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
    /// Gets the difference between this directory and an another directory.
    ///
    /// # Arguments
    ///
    /// * `other` - Identifier of the directory to compare.
    pub fn diff(&self, other: DirectoryId) -> Directory {
        let mut query = self.selection.select("diff");
        query = query.arg("other", other);
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
    pub fn with_directory(&self, path: impl Into<String>, directory: DirectoryId) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
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
        directory: DirectoryId,
        opts: DirectoryWithDirectoryOpts<'a>,
    ) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
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
    pub fn with_file(&self, path: impl Into<String>, source: FileId) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
        source: FileId,
        opts: DirectoryWithFileOpts,
    ) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
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
pub struct Project {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl Project {
    /// Commands provided by this project
    pub fn commands(&self) -> Vec<ProjectCommand> {
        let query = self.selection.select("commands");
        return vec![ProjectCommand {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// A unique identifier for this project.
    pub async fn id(&self) -> Result<ProjectId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// Initialize this project from the given directory and config path
    pub fn load(&self, source: DirectoryId, config_path: impl Into<String>) -> Project {
        let mut query = self.selection.select("load");
        query = query.arg("source", source);
        query = query.arg("configPath", config_path.into());
        return Project {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Name of the project
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
}
#[derive(Clone)]
pub struct ProjectCommand {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ProjectCommand {
    /// Documentation for what this command does.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// Flags accepted by this command.
    pub fn flags(&self) -> Vec<ProjectCommandFlag> {
        let query = self.selection.select("flags");
        return vec![ProjectCommandFlag {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
    /// A unique identifier for this command.
    pub async fn id(&self) -> Result<ProjectCommandId, DaggerError> {
        let query = self.selection.select("id");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the command.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the type returned by this command.
    pub async fn result_type(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("resultType");
        query.execute(self.graphql_client.clone()).await
    }
    /// Subcommands, if any, that this command provides.
    pub fn subcommands(&self) -> Vec<ProjectCommand> {
        let query = self.selection.select("subcommands");
        return vec![ProjectCommand {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        }];
    }
}
#[derive(Clone)]
pub struct ProjectCommandFlag {
    pub proc: Option<Arc<Child>>,
    pub selection: Selection,
    pub graphql_client: DynGraphQLClient,
}
impl ProjectCommandFlag {
    /// Documentation for what this flag sets.
    pub async fn description(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("description");
        query.execute(self.graphql_client.clone()).await
    }
    /// The name of the flag.
    pub async fn name(&self) -> Result<String, DaggerError> {
        let query = self.selection.select("name");
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
pub struct QueryGitOpts {
    /// A service which must be started before the repo is fetched.
    #[builder(setter(into, strip_option), default)]
    pub experimental_service_host: Option<ContainerId>,
    /// Set to true to keep .git directory.
    #[builder(setter(into, strip_option), default)]
    pub keep_git_dir: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryHttpOpts {
    /// A service which must be started before the URL is fetched.
    #[builder(setter(into, strip_option), default)]
    pub experimental_service_host: Option<ContainerId>,
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
pub struct QueryProjectOpts {
    #[builder(setter(into, strip_option), default)]
    pub id: Option<ProjectId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryProjectCommandOpts {
    #[builder(setter(into, strip_option), default)]
    pub id: Option<ProjectCommandId>,
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
    /// Loads a container from ID.
    /// Null ID returns an empty container (scratch).
    /// Optional platform argument initializes new containers to execute and publish as that platform.
    /// Platform defaults to that of the builder's host.
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
    /// Loads a container from ID.
    /// Null ID returns an empty container (scratch).
    /// Optional platform argument initializes new containers to execute and publish as that platform.
    /// Platform defaults to that of the builder's host.
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
    /// The default platform of the builder.
    pub async fn default_platform(&self) -> Result<Platform, DaggerError> {
        let query = self.selection.select("defaultPlatform");
        query.execute(self.graphql_client.clone()).await
    }
    /// Load a directory by ID. No argument produces an empty directory.
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
    /// Load a directory by ID. No argument produces an empty directory.
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
    pub fn file(&self, id: FileId) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("id", id);
        return File {
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
    /// Can be formatted as https://{host}/{owner}/{repo}, git@{host}/{owner}/{repo}
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
    /// Can be formatted as https://{host}/{owner}/{repo}, git@{host}/{owner}/{repo}
    /// Suffix ".git" is optional.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git_opts(&self, url: impl Into<String>, opts: QueryGitOpts) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        if let Some(keep_git_dir) = opts.keep_git_dir {
            query = query.arg("keepGitDir", keep_git_dir);
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
    /// Load a project from ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn project(&self) -> Project {
        let query = self.selection.select("project");
        return Project {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a project from ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn project_opts(&self, opts: QueryProjectOpts) -> Project {
        let mut query = self.selection.select("project");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return Project {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a project command from ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn project_command(&self) -> ProjectCommand {
        let query = self.selection.select("projectCommand");
        return ProjectCommand {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Load a project command from ID.
    ///
    /// # Arguments
    ///
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn project_command_opts(&self, opts: QueryProjectCommandOpts) -> ProjectCommand {
        let mut query = self.selection.select("projectCommand");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return ProjectCommand {
            proc: self.proc.clone(),
            selection: query,
            graphql_client: self.graphql_client.clone(),
        };
    }
    /// Loads a secret from its ID.
    pub fn secret(&self, id: SecretId) -> Secret {
        let mut query = self.selection.select("secret");
        query = query.arg("id", id);
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
