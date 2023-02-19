use crate::client::graphql_client;
use crate::querybuilder::Selection;
use dagger_core::connect_params::ConnectParams;
use derive_builder::Builder;
use serde::{Deserialize, Serialize};
use std::process::Child;
use std::sync::Arc;

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
    pub name: String,
    pub value: String,
}
pub struct CacheVolume {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl CacheVolume {
    pub fn id(&self) -> eyre::Result<CacheId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct Container {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

#[derive(Builder, Debug, PartialEq)]
pub struct ContainerBuildOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub dockerfile: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub build_args: Option<Vec<BuildArg>>,
    #[builder(setter(into, strip_option))]
    pub target: Option<&'a str>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExecOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub args: Option<Vec<&'a str>>,
    #[builder(setter(into, strip_option))]
    pub stdin: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub redirect_stdout: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub redirect_stderr: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub experimental_privileged_nesting: Option<bool>,
}
#[derive(Builder, Debug, PartialEq)]
pub struct ContainerExportOpts {
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
    #[builder(setter(into, strip_option))]
    pub stdin: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub redirect_stdout: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub redirect_stderr: Option<&'a str>,
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
    #[builder(setter(into, strip_option))]
    pub source: Option<DirectoryId>,
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
    pub fn build(&self, context: DirectoryId) -> Container {
        let mut query = self.selection.select("build");

        query = query.arg("context", context);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn build_opts(&self, context: DirectoryId, opts: ContainerBuildOpts) -> Container {
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
    pub fn default_args(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("defaultArgs");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn entrypoint(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("entrypoint");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn env_variable(&self, name: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("envVariable");

        query = query.arg("name", name.into());

        query.execute(&graphql_client(&self.conn))
    }
    pub fn env_variables(&self) -> Vec<EnvVariable> {
        let query = self.selection.select("envVariables");

        return vec![EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn exec(&self) -> Container {
        let query = self.selection.select("exec");

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn exec_opts(&self, opts: ContainerExecOpts) -> Container {
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
    pub fn exit_code(&self) -> eyre::Result<isize> {
        let query = self.selection.select("exitCode");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");

        query = query.arg("path", path.into());

        query.execute(&graphql_client(&self.conn))
    }

    pub fn export_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerExportOpts,
    ) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");

        query = query.arg("path", path.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }

        query.execute(&graphql_client(&self.conn))
    }
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");

        query = query.arg("path", path.into());

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn from(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("from");

        query = query.arg("address", address.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn fs(&self) -> Directory {
        let query = self.selection.select("fs");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn id(&self) -> eyre::Result<ContainerId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn label(&self, name: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("label");

        query = query.arg("name", name.into());

        query.execute(&graphql_client(&self.conn))
    }
    pub fn labels(&self) -> Vec<Label> {
        let query = self.selection.select("labels");

        return vec![Label {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn mounts(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("mounts");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn pipeline(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn pipeline_opts(&self, name: impl Into<String>, opts: ContainerPipelineOpts) -> Container {
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
    pub fn platform(&self) -> eyre::Result<Platform> {
        let query = self.selection.select("platform");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn publish(&self, address: impl Into<String>) -> eyre::Result<String> {
        let mut query = self.selection.select("publish");

        query = query.arg("address", address.into());

        query.execute(&graphql_client(&self.conn))
    }

    pub fn publish_opts(
        &self,
        address: impl Into<String>,
        opts: ContainerPublishOpts,
    ) -> eyre::Result<String> {
        let mut query = self.selection.select("publish");

        query = query.arg("address", address.into());
        if let Some(platform_variants) = opts.platform_variants {
            query = query.arg("platformVariants", platform_variants);
        }

        query.execute(&graphql_client(&self.conn))
    }
    pub fn rootfs(&self) -> Directory {
        let query = self.selection.select("rootfs");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn stderr(&self) -> eyre::Result<String> {
        let query = self.selection.select("stderr");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn stdout(&self) -> eyre::Result<String> {
        let query = self.selection.select("stdout");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn user(&self) -> eyre::Result<String> {
        let query = self.selection.select("user");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn with_default_args(&self) -> Container {
        let query = self.selection.select("withDefaultArgs");

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn with_default_args_opts(&self, opts: ContainerWithDefaultArgsOpts) -> Container {
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

    pub fn with_directory_opts(
        &self,
        path: impl Into<String>,
        directory: DirectoryId,
        opts: ContainerWithDirectoryOpts,
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

    pub fn with_exec_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: ContainerWithExecOpts,
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
    pub fn with_fs(&self, id: DirectoryId) -> Container {
        let mut query = self.selection.select("withFS");

        query = query.arg("id", id);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
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
    pub fn with_mounted_temp(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withMountedTemp");

        query = query.arg("path", path.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_new_file(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withNewFile");

        query = query.arg("path", path.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn with_new_file_opts(
        &self,
        path: impl Into<String>,
        opts: ContainerWithNewFileOpts,
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
    pub fn with_rootfs(&self, id: DirectoryId) -> Container {
        let mut query = self.selection.select("withRootfs");

        query = query.arg("id", id);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
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
    pub fn with_user(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withUser");

        query = query.arg("name", name.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_workdir(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withWorkdir");

        query = query.arg("path", path.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_env_variable(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutEnvVariable");

        query = query.arg("name", name.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_label(&self, name: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutLabel");

        query = query.arg("name", name.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_mount(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutMount");

        query = query.arg("path", path.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_registry_auth(&self, address: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutRegistryAuth");

        query = query.arg("address", address.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_unix_socket(&self, path: impl Into<String>) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");

        query = query.arg("path", path.into());

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn workdir(&self) -> eyre::Result<String> {
        let query = self.selection.select("workdir");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct Directory {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

#[derive(Builder, Debug, PartialEq)]
pub struct DirectoryDockerBuildOpts<'a> {
    #[builder(setter(into, strip_option))]
    pub dockerfile: Option<&'a str>,
    #[builder(setter(into, strip_option))]
    pub platform: Option<Platform>,
    #[builder(setter(into, strip_option))]
    pub build_args: Option<Vec<BuildArg>>,
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
    #[builder(setter(into, strip_option))]
    pub exclude: Option<Vec<&'a str>>,
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
    pub fn diff(&self, other: DirectoryId) -> Directory {
        let mut query = self.selection.select("diff");

        query = query.arg("other", other);

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn docker_build(&self) -> Container {
        let query = self.selection.select("dockerBuild");

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn docker_build_opts(&self, opts: DirectoryDockerBuildOpts) -> Container {
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
    pub fn entries(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("entries");

        query.execute(&graphql_client(&self.conn))
    }

    pub fn entries_opts(&self, opts: DirectoryEntriesOpts) -> eyre::Result<Vec<String>> {
        let mut query = self.selection.select("entries");

        if let Some(path) = opts.path {
            query = query.arg("path", path);
        }

        query.execute(&graphql_client(&self.conn))
    }
    pub fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");

        query = query.arg("path", path.into());

        query.execute(&graphql_client(&self.conn))
    }
    pub fn file(&self, path: impl Into<String>) -> File {
        let mut query = self.selection.select("file");

        query = query.arg("path", path.into());

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn id(&self) -> eyre::Result<DirectoryId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn load_project(&self, config_path: impl Into<String>) -> Project {
        let mut query = self.selection.select("loadProject");

        query = query.arg("configPath", config_path.into());

        return Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn pipeline(&self, name: impl Into<String>) -> Directory {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn pipeline_opts(&self, name: impl Into<String>, opts: DirectoryPipelineOpts) -> Directory {
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

    pub fn with_directory_opts(
        &self,
        path: impl Into<String>,
        directory: DirectoryId,
        opts: DirectoryWithDirectoryOpts,
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
    pub fn with_new_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withNewDirectory");

        query = query.arg("path", path.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

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
    pub fn with_timestamps(&self, timestamp: isize) -> Directory {
        let mut query = self.selection.select("withTimestamps");

        query = query.arg("timestamp", timestamp);

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("withoutDirectory");

        query = query.arg("path", path.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
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
pub struct EnvVariable {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl EnvVariable {
    pub fn name(&self) -> eyre::Result<String> {
        let query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn value(&self) -> eyre::Result<String> {
        let query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct File {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl File {
    pub fn contents(&self) -> eyre::Result<String> {
        let query = self.selection.select("contents");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn export(&self, path: impl Into<String>) -> eyre::Result<bool> {
        let mut query = self.selection.select("export");

        query = query.arg("path", path.into());

        query.execute(&graphql_client(&self.conn))
    }
    pub fn id(&self) -> eyre::Result<FileId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn secret(&self) -> Secret {
        let query = self.selection.select("secret");

        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn size(&self) -> eyre::Result<isize> {
        let query = self.selection.select("size");

        query.execute(&graphql_client(&self.conn))
    }
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
    pub fn digest(&self) -> eyre::Result<String> {
        let query = self.selection.select("digest");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn tree(&self) -> Directory {
        let query = self.selection.select("tree");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn tree_opts(&self, opts: GitRefTreeOpts) -> Directory {
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
pub struct GitRepository {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl GitRepository {
    pub fn branch(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("branch");

        query = query.arg("name", name.into());

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn branches(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("branches");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn commit(&self, id: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("commit");

        query = query.arg("id", id.into());

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn tag(&self, name: impl Into<String>) -> GitRef {
        let mut query = self.selection.select("tag");

        query = query.arg("name", name.into());

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn tags(&self) -> eyre::Result<Vec<String>> {
        let query = self.selection.select("tags");

        query.execute(&graphql_client(&self.conn))
    }
}
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
    pub fn directory(&self, path: impl Into<String>) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path.into());

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn directory_opts(&self, path: impl Into<String>, opts: HostDirectoryOpts) -> Directory {
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
    pub fn env_variable(&self, name: impl Into<String>) -> HostVariable {
        let mut query = self.selection.select("envVariable");

        query = query.arg("name", name.into());

        return HostVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn unix_socket(&self, path: impl Into<String>) -> Socket {
        let mut query = self.selection.select("unixSocket");

        query = query.arg("path", path.into());

        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn workdir(&self) -> Directory {
        let query = self.selection.select("workdir");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn workdir_opts(&self, opts: HostWorkdirOpts) -> Directory {
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
pub struct HostVariable {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl HostVariable {
    pub fn secret(&self) -> Secret {
        let query = self.selection.select("secret");

        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn value(&self) -> eyre::Result<String> {
        let query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct Label {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Label {
    pub fn name(&self) -> eyre::Result<String> {
        let query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn value(&self) -> eyre::Result<String> {
        let query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct Project {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Project {
    pub fn extensions(&self) -> Vec<Project> {
        let query = self.selection.select("extensions");

        return vec![Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn generated_code(&self) -> Directory {
        let query = self.selection.select("generatedCode");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn install(&self) -> eyre::Result<bool> {
        let query = self.selection.select("install");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn name(&self) -> eyre::Result<String> {
        let query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn schema(&self) -> eyre::Result<String> {
        let query = self.selection.select("schema");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn sdk(&self) -> eyre::Result<String> {
        let query = self.selection.select("sdk");

        query.execute(&graphql_client(&self.conn))
    }
}
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
    pub fn cache_volume(&self, key: impl Into<String>) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");

        query = query.arg("key", key.into());

        return CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn container(&self) -> Container {
        let query = self.selection.select("container");

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

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
    pub fn default_platform(&self) -> eyre::Result<Platform> {
        let query = self.selection.select("defaultPlatform");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn directory(&self) -> Directory {
        let query = self.selection.select("directory");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

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
    pub fn file(&self, id: FileId) -> File {
        let mut query = self.selection.select("file");

        query = query.arg("id", id);

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn git(&self, url: impl Into<String>) -> GitRepository {
        let mut query = self.selection.select("git");

        query = query.arg("url", url.into());

        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

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
    pub fn host(&self) -> Host {
        let query = self.selection.select("host");

        return Host {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn http(&self, url: impl Into<String>) -> File {
        let mut query = self.selection.select("http");

        query = query.arg("url", url.into());

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn pipeline(&self, name: impl Into<String>) -> Query {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name.into());

        return Query {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

    pub fn pipeline_opts(&self, name: impl Into<String>, opts: QueryPipelineOpts) -> Query {
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
    pub fn project(&self, name: impl Into<String>) -> Project {
        let mut query = self.selection.select("project");

        query = query.arg("name", name.into());

        return Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn secret(&self, id: SecretId) -> Secret {
        let mut query = self.selection.select("secret");

        query = query.arg("id", id);

        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn socket(&self) -> Socket {
        let query = self.selection.select("socket");

        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }

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
pub struct Secret {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Secret {
    pub fn id(&self) -> eyre::Result<SecretId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
    pub fn plaintext(&self) -> eyre::Result<String> {
        let query = self.selection.select("plaintext");

        query.execute(&graphql_client(&self.conn))
    }
}
pub struct Socket {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Socket {
    pub fn id(&self) -> eyre::Result<SocketId> {
        let query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn))
    }
}
#[derive(Serialize, Clone, PartialEq, Debug)]
pub enum CacheSharingMode {
    SHARED,
    PRIVATE,
    LOCKED,
}
