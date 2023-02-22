use crate::client::graphql_client;
use crate::querybuilder::Selection;
use dagger_core::connect_params::ConnectParams;
use derive_builder::Builder;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tokio::process::Child;

#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct CacheId(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct ContainerId(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct DirectoryId(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct FileId(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct Platform(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SecretId(String);
#[derive(Serialize, Deserialize, PartialEq, Debug, Clone)]
pub struct SocketId(String);
#[derive(Serialize, Deserialize, Debug, PartialEq, Clone)]
pub struct BuildArg {
    pub value: String,
    pub name: String,
}
#[derive(Debug, Clone)]
pub struct CacheVolume {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl CacheVolume {
    pub async fn id(&self) -> eyre::Result<CacheId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Container {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerBuildOpts<'a> {
    /// Path to the Dockerfile to use.
    /// Defaults to './Dockerfile'.
    #[builder(setter(into, strip_option))]
    pub dockerfile: Option<&'a str>,
    /// Additional build arguments.
    #[builder(setter(into, strip_option))]
    pub build_args: Option<Vec<BuildArg>>,
    /// Target build stage to build.
    #[builder(setter(into, strip_option))]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExecOpts<'a> {
    /// Command to run instead of the container's default command.
    #[builder(setter(into, strip_option))]
    pub args: Option<Vec<&'a str>>,
    /// Content to write to the command's standard input before closing.
    #[builder(setter(into, strip_option))]
    pub stdin: Option<&'a str>,
    /// Redirect the command's standard output to a file in the container.
    #[builder(setter(into, strip_option))]
    pub redirect_stdout: Option<&'a str>,
    /// Redirect the command's standard error to a file in the container.
    #[builder(setter(into, strip_option))]
    pub redirect_stderr: Option<&'a str>,
    /// Provide dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed.
    /// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option))]
    pub experimental_privileged_nesting: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExportOpts {
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option))]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerPipelineOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub description: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerPublishOpts {
    /// Identifiers for other platform specific containers.
    /// Used for multi-platform image.
    #[builder(setter(into, strip_option))]
    pub platform_variants: Option<Vec<ContainerId>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDefaultArgsOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub args: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithDirectoryOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub exclude: Option<Vec<&'a str>>,
    #[builder(setter(into, strip_option))]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithExecOpts<'a> {
    /// Content to write to the command's standard input before closing.
    #[builder(setter(into, strip_option))]
    pub stdin: Option<&'a str>,
    /// Redirect the command's standard output to a file in the container.
    #[builder(setter(into, strip_option))]
    pub redirect_stdout: Option<&'a str>,
    /// Redirect the command's standard error to a file in the container.
    #[builder(setter(into, strip_option))]
    pub redirect_stderr: Option<&'a str>,
    /// Provide dagger access to the executed command.
    /// Do not use this option unless you trust the command being executed.
    /// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM.
    #[builder(setter(into, strip_option))]
    pub experimental_privileged_nesting: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithFileOpts {
    #[builder(setter(into, strip_option))]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithMountedCacheOpts {
    /// Directory to use as the cache volume's root.
    #[builder(setter(into, strip_option))]
    pub source: Option<DirectoryId>,
    /// Sharing mode of the cache volume.
    #[builder(setter(into, strip_option))]
    pub sharing: Option<CacheSharingMode>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerWithNewFileOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub contents: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub permissions: Option<isize>,
}
impl Container {
    /// Initializes this container from a Dockerfile build, using the context, a dockerfile file path and some additional buildArgs.
    ///  /// # Arguments ///  /// * `context` - Directory context used by the Dockerfile.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn build(&self, context: DirectoryId) -> Container {
        let mut query = self.selection.select("build");
        query = query.arg("context", context);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Initializes this container from a Dockerfile build, using the context, a dockerfile file path and some additional buildArgs.
    ///  /// # Arguments ///  /// * `context` - Directory context used by the Dockerfile.
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves default arguments for future commands.
    pub async fn default_args(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("defaultArgs");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves a directory at the given path. Mounts are included.
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves entrypoint to be prepended to the arguments of all commands.
    pub async fn entrypoint(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("entrypoint");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the value of the specified environment variable.
    pub async fn env_variable(&self, name: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("envVariable");
        query = query.arg("name", name.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the list of environment variables passed to commands.
    pub fn env_variables(&self) -> Vec<EnvVariable> {
        let mut query = self.selection.select("envVariables");
        return vec![EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    /// Retrieves this container after executing the specified command inside it.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn exec(&self) -> Container {
        let mut query = self.selection.select("exec");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container after executing the specified command inside it.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn exec_opts<'a>(&self, opts: ContainerExecOpts<'a>) -> Container {
        let mut query = self.selection.select("exec");
        if let Some(args) = opts.args {
            query = query.arg("args", args);
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Exit code of the last executed command. Zero means success.
    /// Null if no command has been executed.
    pub async fn exit_code(&self) -> eyre::Result<isize> {
        let mut query = self.selection.select("exitCode");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Writes the container as an OCI tarball to the destination file path on the host for the specified platformVariants.
    /// Return true on success.
    ///  /// # Arguments ///  /// * `path` - Host's destination path.
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Writes the container as an OCI tarball to the destination file path on the host for the specified platformVariants.
    /// Return true on success.
    ///  /// # Arguments ///  /// * `path` - Host's destination path.
    /// Path can be relative to the engine's workdir or absolute.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerExportOpts,
    ) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves a file at the given path. Mounts are included.
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Initializes this container from the base image published at the given address.
    ///  /// # Arguments ///  /// * `address` - Image's address from its registry.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    pub fn from(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("from");
        query = query.arg("address", address.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container's root filesystem. Mounts are not included.
    pub fn fs(&self) -> Directory {
        let mut query = self.selection.select("fs");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// A unique identifier for this container.
    pub async fn id(&self) -> eyre::Result<ContainerId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the value of the specified label.
    pub async fn label(&self, name: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("label");
        query = query.arg("name", name.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the list of labels passed to container.
    pub fn labels(&self) -> Vec<Label> {
        let mut query = self.selection.select("labels");
        return vec![Label {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    /// Retrieves the list of paths where a directory is mounted.
    pub async fn mounts(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("mounts");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Creates a named sub-pipeline
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Creates a named sub-pipeline
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// The platform this container executes and publishes as.
    pub async fn platform(&self) -> eyre::Result<Platform> {
        let mut query = self.selection.select("platform");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Publishes this container as a new image to the specified address, for the platformVariants, returning a fully qualified ref.
    ///  /// # Arguments ///  /// * `address` - Registry's address to publish the image to.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn publish(&self, address: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("publish");
        query = query.arg("address", address.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Publishes this container as a new image to the specified address, for the platformVariants, returning a fully qualified ref.
    ///  /// # Arguments ///  /// * `address` - Registry's address to publish the image to.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn publish_opts(
        &self,
        address: impl Into<String>,
        opts: ContainerPublishOpts,
    ) -> eyre::Result<String> {
        let mut query = self.selection.select("publish");
        query = query.arg("address", address.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves this container's root filesystem. Mounts are not included.
    pub fn rootfs(&self) -> Directory {
        let mut query = self.selection.select("rootfs");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// The error stream of the last executed command.
    /// Null if no command has been executed.
    pub async fn stderr(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("stderr");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// The output stream of the last executed command.
    /// Null if no command has been executed.
    pub async fn stdout(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("stdout");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the user to be set for all commands.
    pub async fn user(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("user");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Configures default arguments for future commands.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_args(&self) -> Container {
        let mut query = self.selection.select("withDefaultArgs");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Configures default arguments for future commands.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_default_args_opts<'a>(&self, opts: ContainerWithDefaultArgsOpts<'a>) -> Container {
        let mut query = self.selection.select("withDefaultArgs");
        if let Some(args) = opts.args {
            query = query.arg("args", args);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a directory written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(&self, path: impl Into<String>, directory: DirectoryId) -> Container {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a directory written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container but with a different command entrypoint.
    pub fn with_entrypoint(&self, args: Vec<impl Into<String>>) -> Container {
        let mut query = self.selection.select("withEntrypoint");
        query = query.arg(
            "args",
            args.into_iter().map(|i| i.into()).collect::<Vec<String>>(),
        );
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus the given environment variable.
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container after executing the specified command inside it.
    ///  /// # Arguments ///  /// * `args` - Command to run instead of the container's default command.
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container after executing the specified command inside it.
    ///  /// # Arguments ///  /// * `args` - Command to run instead of the container's default command.
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Initializes this container from this DirectoryID.
    pub fn with_fs(&self, id: DirectoryId) -> Container {
        let mut query = self.selection.select("withFS");
        query = query.arg("id", id);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus the contents of the given file copied to the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: FileId) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus the contents of the given file copied to the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file_opts(
        &self,
        path: impl Into<String>,
        source: FileId,
        opts: ContainerWithFileOpts,
    ) -> Container {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        if let Some(permissions) = opts.permissions {
            query = query.arg("permissions", permissions);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus the given label.
    pub fn with_label(&self, name: impl Into<String>, value: impl Into<String>) -> Container {
        let mut query = self.selection.select("withLabel");
        query = query.arg("name", name.into());
        query = query.arg("value", value.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///  /// # Arguments ///  /// * `path` - Path to mount the cache volume at.
    /// * `cache` - ID of the cache to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache(&self, path: impl Into<String>, cache: CacheId) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg("cache", cache);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a cache volume mounted at the given path.
    ///  /// # Arguments ///  /// * `path` - Path to mount the cache volume at.
    /// * `cache` - ID of the cache to mount.
    /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_mounted_cache_opts(
        &self,
        path: impl Into<String>,
        cache: CacheId,
        opts: ContainerWithMountedCacheOpts,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");
        query = query.arg("path", path.into());
        query = query.arg("cache", cache);
        if let Some(source) = opts.source {
            query = query.arg("source", source);
        }
        if let Some(sharing) = opts.sharing {
            query = query.arg("sharing", sharing);
        }
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a directory mounted at the given path.
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a file mounted at the given path.
    pub fn with_mounted_file(&self, path: impl Into<String>, source: FileId) -> Container {
        let mut query = self.selection.select("withMountedFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a secret mounted into a file at the given path.
    pub fn with_mounted_secret(&self, path: impl Into<String>, source: SecretId) -> Container {
        let mut query = self.selection.select("withMountedSecret");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a temporary directory mounted at the given path.
    pub fn with_mounted_temp(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withMountedTemp");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a new file written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a new file written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container with a registry authentication for a given address.
    ///  /// # Arguments ///  /// * `address` - Registry's address to bind the authentication to.
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
            conn: self.conn.clone(),
        };
    }
    /// Initializes this container from this DirectoryID.
    pub fn with_rootfs(&self, id: DirectoryId) -> Container {
        let mut query = self.selection.select("withRootfs");
        query = query.arg("id", id);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus an env variable containing the given secret.
    pub fn with_secret_variable(&self, name: impl Into<String>, secret: SecretId) -> Container {
        let mut query = self.selection.select("withSecretVariable");
        query = query.arg("name", name.into());
        query = query.arg("secret", secret);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container plus a socket forwarded to the given Unix socket path.
    pub fn with_unix_socket(&self, path: impl Into<String>, source: SocketId) -> Container {
        let mut query = self.selection.select("withUnixSocket");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this containers with a different command user.
    pub fn with_user(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withUser");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container with a different working directory.
    pub fn with_workdir(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withWorkdir");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container minus the given environment variable.
    pub fn without_env_variable(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutEnvVariable");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container minus the given environment label.
    pub fn without_label(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutLabel");
        query = query.arg("name", name.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container after unmounting everything at the given path.
    pub fn without_mount(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutMount");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container without the registry authentication of a given address.
    ///  /// # Arguments ///  /// * `address` - Registry's address to remove the authentication from.
    /// Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
    pub fn without_registry_auth(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutRegistryAuth");
        query = query.arg("address", address.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this container with a previously added Unix socket removed.
    pub fn without_unix_socket(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");
        query = query.arg("path", path.into());
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves the working directory for all commands.
    pub async fn workdir(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("workdir");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Directory {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryDockerBuildOpts<'a> {
    /// Path to the Dockerfile to use.
    /// Defaults to './Dockerfile'.
    #[builder(setter(into, strip_option))]
    pub dockerfile: Option<&'a str>,
    /// The platform to build.
    #[builder(setter(into, strip_option))]
    pub platform: Option<Platform>,
    /// Additional build arguments.
    #[builder(setter(into, strip_option))]
    pub build_args: Option<Vec<BuildArg>>,
    /// Target build stage to build.
    #[builder(setter(into, strip_option))]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryEntriesOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub path: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryPipelineOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub description: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithDirectoryOpts<'a> {
    /// Exclude artifacts that match the given pattern.
    /// (e.g. ["node_modules/", ".git*"]).
    #[builder(setter(into, strip_option))]
    pub exclude: Option<Vec<&'a str>>,
    /// Include only artifacts that match the given pattern.
    /// (e.g. ["app/", "package.*"]).
    #[builder(setter(into, strip_option))]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithFileOpts {
    #[builder(setter(into, strip_option))]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewDirectoryOpts {
    #[builder(setter(into, strip_option))]
    pub permissions: Option<isize>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryWithNewFileOpts {
    #[builder(setter(into, strip_option))]
    pub permissions: Option<isize>,
}
impl Directory {
    /// Gets the difference between this directory and an another directory.
    pub fn diff(&self, other: DirectoryId) -> Directory {
        let mut query = self.selection.select("diff");
        query = query.arg("other", other);
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves a directory at the given path.
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Builds a new Docker container from this directory.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn docker_build(&self) -> Container {
        let mut query = self.selection.select("dockerBuild");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Builds a new Docker container from this directory.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Returns a list of files and directories at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn entries(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("entries");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Returns a list of files and directories at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub async fn entries_opts<'a>(
        &self,
        opts: DirectoryEntriesOpts<'a>,
    ) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("entries");
        if let Some(path) = opts.path {
            query = query.arg("path", path);
        }
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Writes the contents of the directory to a path on the host.
    pub async fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves a file at the given path.
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("path", path.into());
        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// The content-addressed identifier of the directory.
    pub async fn id(&self) -> eyre::Result<DirectoryId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// load a project's metadata
    pub fn load_project(&self, config_path: impl Into<String>) -> Project {
        let mut query = self.selection.select("loadProject");
        query = query.arg("configPath", config_path.into());
        return Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Creates a named sub-pipeline.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Directory {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Creates a named sub-pipeline.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a directory written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_directory(&self, path: impl Into<String>, directory: DirectoryId) -> Directory {
        let mut query = self.selection.select("withDirectory");
        query = query.arg("path", path.into());
        query = query.arg("directory", directory);
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a directory written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_file(&self, path: impl Into<String>, source: FileId) -> Directory {
        let mut query = self.selection.select("withFile");
        query = query.arg("path", path.into());
        query = query.arg("source", source);
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus the contents of the given file copied to the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a new directory created at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withNewDirectory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a new directory created at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a new file written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn with_new_file(&self, path: impl Into<String>, contents: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withNewFile");
        query = query.arg("path", path.into());
        query = query.arg("contents", contents.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory plus a new file written at the given path.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory with all file/dir timestamps set to the given time, in seconds from the Unix epoch.
    pub fn with_timestamps(&self, timestamp: isize) -> Directory {
        let mut query = self.selection.select("withTimestamps");
        query = query.arg("timestamp", timestamp);
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory with the directory at the given path removed.
    pub fn without_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutDirectory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves this directory with the file at the given path removed.
    pub fn without_file(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutFile");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
}
#[derive(Debug, Clone)]
pub struct EnvVariable {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl EnvVariable {
    /// The environment variable name.
    pub async fn name(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("name");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// The environment variable value.
    pub async fn value(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("value");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct File {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl File {
    /// Retrieves the contents of the file.
    pub async fn contents(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("contents");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Writes the file to a file path on the host.
    pub async fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");
        query = query.arg("path", path.into());
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves the content-addressed identifier of the file.
    pub async fn id(&self) -> eyre::Result<FileId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves a secret referencing the contents of this file.
    pub fn secret(&self) -> Secret {
        let mut query = self.selection.select("secret");
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Gets the size of the file, in bytes.
    pub async fn size(&self) -> eyre::Result<isize> {
        let mut query = self.selection.select("size");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Retrieves this file with its created/modified timestamps set to the given time, in seconds from the Unix epoch.
    pub fn with_timestamps(&self, timestamp: isize) -> File {
        let mut query = self.selection.select("withTimestamps");
        query = query.arg("timestamp", timestamp);
        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
}
#[derive(Debug, Clone)]
pub struct GitRef {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
#[derive(Builder, Debug, PartialEq)]
pub struct GitRefTreeOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub ssh_known_hosts: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub ssh_auth_socket: Option<SocketId>,
}
impl GitRef {
    /// The digest of the current value of this ref.
    pub async fn digest(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("digest");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// The filesystem tree at this ref.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn tree(&self) -> Directory {
        let mut query = self.selection.select("tree");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// The filesystem tree at this ref.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
}
#[derive(Debug, Clone)]
pub struct GitRepository {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl GitRepository {
    /// Returns details on one branch.
    pub fn branch(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("branch");
        query = query.arg("name", name.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Lists of branches on the repository.
    pub async fn branches(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("branches");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Returns details on one commit.
    pub fn commit(&self, id: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("commit");
        query = query.arg("id", id.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Returns details on one tag.
    pub fn tag(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("tag");
        query = query.arg("name", name.into());
        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Lists of tags on the repository.
    pub async fn tags(&self) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("tags");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Host {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
#[derive(Builder, Debug, PartialEq)]
pub struct HostDirectoryOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub exclude: Option<Vec<&'a str>>,
    #[builder(setter(into, strip_option))]
    pub include: Option<Vec<&'a str>>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct HostWorkdirOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub exclude: Option<Vec<&'a str>>,
    #[builder(setter(into, strip_option))]
    pub include: Option<Vec<&'a str>>,
}
impl Host {
    /// Accesses a directory on the host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");
        query = query.arg("path", path.into());
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Accesses a directory on the host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// Accesses an environment variable on the host.
    pub fn env_variable(&self, name: impl Into<String>) -> HostVariable {
        let mut query = self.selection.select("envVariable");
        query = query.arg("name", name.into());
        return HostVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Accesses a Unix socket on the host.
    pub fn unix_socket(&self, path: impl Into<String>) -> Socket {
        let mut query = self.selection.select("unixSocket");
        query = query.arg("path", path.into());
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves the current working directory on the host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn workdir(&self) -> Directory {
        let mut query = self.selection.select("workdir");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Retrieves the current working directory on the host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn workdir_opts<'a>(&self, opts: HostWorkdirOpts<'a>) -> Directory {
        let mut query = self.selection.select("workdir");
        if let Some(exclude) = opts.exclude {
            query = query.arg("exclude", exclude);
        }
        if let Some(include) = opts.include {
            query = query.arg("include", include);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
}
#[derive(Debug, Clone)]
pub struct HostVariable {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl HostVariable {
    /// A secret referencing the value of this variable.
    pub fn secret(&self) -> Secret {
        let mut query = self.selection.select("secret");
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// The value of this variable.
    pub async fn value(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("value");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Label {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl Label {
    /// The label name.
    pub async fn name(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("name");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// The label value.
    pub async fn value(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("value");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Project {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl Project {
    /// extensions in this project
    pub fn extensions(&self) -> Vec<Project> {
        let mut query = self.selection.select("extensions");
        return vec![Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    /// Code files generated by the SDKs in the project
    pub fn generated_code(&self) -> Directory {
        let mut query = self.selection.select("generatedCode");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// install the project's schema
    pub async fn install(&self) -> eyre::Result<bool> {
        let mut query = self.selection.select("install");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// name of the project
    pub async fn name(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("name");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// schema provided by the project
    pub async fn schema(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("schema");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// sdk used to generate code for and/or execute this project
    pub async fn sdk(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("sdk");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Query {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryContainerOpts {
    #[builder(setter(into, strip_option))]
    pub id: Option<ContainerId>,
    #[builder(setter(into, strip_option))]
    pub platform: Option<Platform>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryDirectoryOpts {
    #[builder(setter(into, strip_option))]
    pub id: Option<DirectoryId>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryGitOpts {
    #[builder(setter(into, strip_option))]
    pub keep_git_dir: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QueryPipelineOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub description: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct QuerySocketOpts {
    #[builder(setter(into, strip_option))]
    pub id: Option<SocketId>,
}
impl Query {
    /// Constructs a cache volume for a given cache key.
    ///  /// # Arguments ///  /// * `key` - A string identifier to target this cache volume (e.g. "myapp-cache").
    pub fn cache_volume(&self, key: impl Into<String>) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");
        query = query.arg("key", key.into());
        return CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a container from ID.
    /// Null ID returns an empty container (scratch).
    /// Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn container(&self) -> Container {
        let mut query = self.selection.select("container");
        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a container from ID.
    /// Null ID returns an empty container (scratch).
    /// Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
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
            conn: self.conn.clone(),
        };
    }
    /// The default platform of the builder.
    pub async fn default_platform(&self) -> eyre::Result<Platform> {
        let mut query = self.selection.select("defaultPlatform");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// Load a directory by ID. No argument produces an empty directory.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory(&self) -> Directory {
        let mut query = self.selection.select("directory");
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Load a directory by ID. No argument produces an empty directory.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn directory_opts(&self, opts: QueryDirectoryOpts) -> Directory {
        let mut query = self.selection.select("directory");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a file by ID.
    pub fn file(&self, id: FileId) -> File {
        let mut query = self.selection.select("file");
        query = query.arg("id", id);
        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Queries a git repository.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git(&self, url: impl Into<String>) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Queries a git repository.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn git_opts(&self, url: impl Into<String>, opts: QueryGitOpts) -> GitRepository {
        let mut query = self.selection.select("git");
        query = query.arg("url", url.into());
        if let Some(keep_git_dir) = opts.keep_git_dir {
            query = query.arg("keepGitDir", keep_git_dir);
        }
        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Queries the host environment.
    pub fn host(&self) -> Host {
        let mut query = self.selection.select("host");
        return Host {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Returns a file containing an http remote url content.
    pub fn http(&self, url: impl Into<String>) -> File {
        let mut query = self.selection.select("http");
        query = query.arg("url", url.into());
        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Creates a named sub-pipeline
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline(&self, name: impl Into<String>) -> Query {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        return Query {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Creates a named sub-pipeline
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn pipeline_opts<'a>(&self, name: impl Into<String>, opts: QueryPipelineOpts<'a>) -> Query {
        let mut query = self.selection.select("pipeline");
        query = query.arg("name", name.into());
        if let Some(description) = opts.description {
            query = query.arg("description", description);
        }
        return Query {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Look up a project by name
    pub fn project(&self, name: impl Into<String>) -> Project {
        let mut query = self.selection.select("project");
        query = query.arg("name", name.into());
        return Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a secret from its ID.
    pub fn secret(&self, id: SecretId) -> Secret {
        let mut query = self.selection.select("secret");
        query = query.arg("id", id);
        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a socket by its ID.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn socket(&self) -> Socket {
        let mut query = self.selection.select("socket");
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    /// Loads a socket by its ID.
    ///  /// # Arguments ///  /// * `opt` - optional argument, see inner type for documentation, use <func>_opts to use
    pub fn socket_opts(&self, opts: QuerySocketOpts) -> Socket {
        let mut query = self.selection.select("socket");
        if let Some(id) = opts.id {
            query = query.arg("id", id);
        }
        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
}
#[derive(Debug, Clone)]
pub struct Secret {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl Secret {
    /// The identifier for this secret.
    pub async fn id(&self) -> eyre::Result<SecretId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
    /// The value of this secret.
    pub async fn plaintext(&self) -> eyre::Result<String> {
        let mut query = self.selection.select("plaintext");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Debug, Clone)]
pub struct Socket {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}
impl Socket {
    /// The content-addressed identifier of the socket.
    pub async fn id(&self) -> eyre::Result<SocketId> {
        let mut query = self.selection.select("id");
        query.execute(&graphql_client(&self.conn)).await
    }
}
#[derive(Serialize, Clone, PartialEq, Debug)]
pub enum CacheSharingMode {
    PRIVATE,
    LOCKED,
    SHARED,
}
