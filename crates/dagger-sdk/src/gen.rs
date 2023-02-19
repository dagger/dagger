use crate::client::graphql_client;
use crate::querybuilder::Selection;
use dagger_core::connect_params::ConnectParams;
use serde::{Deserialize, Serialize};
use std::process::Child;
use std::sync::Arc;

#[derive(Serialize, Deserialize)]
pub struct CacheId(String);
#[derive(Serialize, Deserialize)]
pub struct ContainerId(String);
#[derive(Serialize, Deserialize)]
pub struct DirectoryId(String);
#[derive(Serialize, Deserialize)]
pub struct FileId(String);
#[derive(Serialize, Deserialize)]
pub struct Platform(String);
#[derive(Serialize, Deserialize)]
pub struct SecretId(String);
#[derive(Serialize, Deserialize)]
pub struct SocketId(String);
#[derive(Serialize, Deserialize)]
pub struct BuildArg {
    pub value: String,
    pub name: String,
}
pub struct CacheVolume {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl CacheVolume {
    pub fn id(&self) -> CacheId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Container {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

pub struct ContainerBuildOpts {
    pub dockerfile: Option<String>,
    pub build_args: Option<Vec<BuildArg>>,
    pub target: Option<String>,
}
pub struct ContainerExecOpts {
    pub args: Option<Vec<String>>,
    pub stdin: Option<String>,
    pub redirect_stdout: Option<String>,
    pub redirect_stderr: Option<String>,
    pub experimental_privileged_nesting: Option<bool>,
}
pub struct ContainerExportOpts {
    pub platform_variants: Option<Vec<ContainerId>>,
}
pub struct ContainerPipelineOpts {
    pub description: Option<String>,
}
pub struct ContainerPublishOpts {
    pub platform_variants: Option<Vec<ContainerId>>,
}
pub struct ContainerWithDefaultArgsOpts {
    pub args: Option<Vec<String>>,
}
pub struct ContainerWithDirectoryOpts {
    pub exclude: Option<Vec<String>>,
    pub include: Option<Vec<String>>,
}
pub struct ContainerWithExecOpts {
    pub stdin: Option<String>,
    pub redirect_stdout: Option<String>,
    pub redirect_stderr: Option<String>,
    pub experimental_privileged_nesting: Option<bool>,
}
pub struct ContainerWithFileOpts {
    pub permissions: Option<isize>,
}
pub struct ContainerWithMountedCacheOpts {
    pub source: Option<DirectoryId>,
    pub sharing: Option<CacheSharingMode>,
}
pub struct ContainerWithNewFileOpts {
    pub contents: Option<String>,
    pub permissions: Option<isize>,
}

impl Container {
    pub fn build(&self, context: DirectoryId, opts: Option<ContainerBuildOpts>) -> Container {
        let mut query = self.selection.select("build");

        query = query.arg("context", context);
        if let Some(opts) = opts {
            if let Some(dockerfile) = opts.dockerfile {
                query = query.arg("dockerfile", dockerfile);
            }
            if let Some(build_args) = opts.build_args {
                query = query.arg("buildArgs", build_args);
            }
            if let Some(target) = opts.target {
                query = query.arg("target", target);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn default_args(&self) -> Vec<String> {
        let mut query = self.selection.select("defaultArgs");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn directory(&self, path: String) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path);

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn entrypoint(&self) -> Vec<String> {
        let mut query = self.selection.select("entrypoint");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn env_variable(&self, name: String) -> String {
        let mut query = self.selection.select("envVariable");

        query = query.arg("name", name);

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn env_variables(&self) -> Vec<EnvVariable> {
        let mut query = self.selection.select("envVariables");

        return vec![EnvVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn exec(&self, opts: Option<ContainerExecOpts>) -> Container {
        let mut query = self.selection.select("exec");

        if let Some(opts) = opts {
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
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn exit_code(&self) -> isize {
        let mut query = self.selection.select("exitCode");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn export(&self, path: String, opts: Option<ContainerExportOpts>) -> bool {
        let mut query = self.selection.select("export");

        query = query.arg("path", path);
        if let Some(opts) = opts {
            if let Some(platform_variants) = opts.platform_variants {
                query = query.arg("platformVariants", platform_variants);
            }
        }

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn file(&self, path: String) -> File {
        let mut query = self.selection.select("file");

        query = query.arg("path", path);

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn from(&self, address: String) -> Container {
        let mut query = self.selection.select("from");

        query = query.arg("address", address);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn fs(&self) -> Directory {
        let mut query = self.selection.select("fs");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn id(&self) -> ContainerId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn label(&self, name: String) -> String {
        let mut query = self.selection.select("label");

        query = query.arg("name", name);

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn labels(&self) -> Vec<Label> {
        let mut query = self.selection.select("labels");

        return vec![Label {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn mounts(&self) -> Vec<String> {
        let mut query = self.selection.select("mounts");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn pipeline(&self, name: String, opts: Option<ContainerPipelineOpts>) -> Container {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name);
        if let Some(opts) = opts {
            if let Some(description) = opts.description {
                query = query.arg("description", description);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn platform(&self) -> Platform {
        let mut query = self.selection.select("platform");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn publish(&self, address: String, opts: Option<ContainerPublishOpts>) -> String {
        let mut query = self.selection.select("publish");

        query = query.arg("address", address);
        if let Some(opts) = opts {
            if let Some(platform_variants) = opts.platform_variants {
                query = query.arg("platformVariants", platform_variants);
            }
        }

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn rootfs(&self) -> Directory {
        let mut query = self.selection.select("rootfs");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn stderr(&self) -> String {
        let mut query = self.selection.select("stderr");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn stdout(&self) -> String {
        let mut query = self.selection.select("stdout");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn user(&self) -> String {
        let mut query = self.selection.select("user");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn with_default_args(&self, opts: Option<ContainerWithDefaultArgsOpts>) -> Container {
        let mut query = self.selection.select("withDefaultArgs");

        if let Some(opts) = opts {
            if let Some(args) = opts.args {
                query = query.arg("args", args);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_directory(
        &self,
        path: String,
        directory: DirectoryId,
        opts: Option<ContainerWithDirectoryOpts>,
    ) -> Container {
        let mut query = self.selection.select("withDirectory");

        query = query.arg("path", path);
        query = query.arg("directory", directory);
        if let Some(opts) = opts {
            if let Some(exclude) = opts.exclude {
                query = query.arg("exclude", exclude);
            }
            if let Some(include) = opts.include {
                query = query.arg("include", include);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_entrypoint(&self, args: Vec<String>) -> Container {
        let mut query = self.selection.select("withEntrypoint");

        query = query.arg("args", args);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_env_variable(&self, name: String, value: String) -> Container {
        let mut query = self.selection.select("withEnvVariable");

        query = query.arg("name", name);
        query = query.arg("value", value);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_exec(&self, args: Vec<String>, opts: Option<ContainerWithExecOpts>) -> Container {
        let mut query = self.selection.select("withExec");

        query = query.arg("args", args);
        if let Some(opts) = opts {
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
    pub fn with_file(
        &self,
        path: String,
        source: FileId,
        opts: Option<ContainerWithFileOpts>,
    ) -> Container {
        let mut query = self.selection.select("withFile");

        query = query.arg("path", path);
        query = query.arg("source", source);
        if let Some(opts) = opts {
            if let Some(permissions) = opts.permissions {
                query = query.arg("permissions", permissions);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_label(&self, name: String, value: String) -> Container {
        let mut query = self.selection.select("withLabel");

        query = query.arg("name", name);
        query = query.arg("value", value);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_mounted_cache(
        &self,
        path: String,
        cache: CacheId,
        opts: Option<ContainerWithMountedCacheOpts>,
    ) -> Container {
        let mut query = self.selection.select("withMountedCache");

        query = query.arg("path", path);
        query = query.arg("cache", cache);
        if let Some(opts) = opts {
            if let Some(source) = opts.source {
                query = query.arg("source", source);
            }
            if let Some(sharing) = opts.sharing {
                query = query.arg("sharing", sharing);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_mounted_directory(&self, path: String, source: DirectoryId) -> Container {
        let mut query = self.selection.select("withMountedDirectory");

        query = query.arg("path", path);
        query = query.arg("source", source);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_mounted_file(&self, path: String, source: FileId) -> Container {
        let mut query = self.selection.select("withMountedFile");

        query = query.arg("path", path);
        query = query.arg("source", source);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_mounted_secret(&self, path: String, source: SecretId) -> Container {
        let mut query = self.selection.select("withMountedSecret");

        query = query.arg("path", path);
        query = query.arg("source", source);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_mounted_temp(&self, path: String) -> Container {
        let mut query = self.selection.select("withMountedTemp");

        query = query.arg("path", path);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_new_file(&self, path: String, opts: Option<ContainerWithNewFileOpts>) -> Container {
        let mut query = self.selection.select("withNewFile");

        query = query.arg("path", path);
        if let Some(opts) = opts {
            if let Some(contents) = opts.contents {
                query = query.arg("contents", contents);
            }
            if let Some(permissions) = opts.permissions {
                query = query.arg("permissions", permissions);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_registry_auth(
        &self,
        address: String,
        username: String,
        secret: SecretId,
    ) -> Container {
        let mut query = self.selection.select("withRegistryAuth");

        query = query.arg("address", address);
        query = query.arg("username", username);
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
    pub fn with_secret_variable(&self, name: String, secret: SecretId) -> Container {
        let mut query = self.selection.select("withSecretVariable");

        query = query.arg("name", name);
        query = query.arg("secret", secret);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_unix_socket(&self, path: String, source: SocketId) -> Container {
        let mut query = self.selection.select("withUnixSocket");

        query = query.arg("path", path);
        query = query.arg("source", source);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_user(&self, name: String) -> Container {
        let mut query = self.selection.select("withUser");

        query = query.arg("name", name);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_workdir(&self, path: String) -> Container {
        let mut query = self.selection.select("withWorkdir");

        query = query.arg("path", path);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_env_variable(&self, name: String) -> Container {
        let mut query = self.selection.select("withoutEnvVariable");

        query = query.arg("name", name);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_label(&self, name: String) -> Container {
        let mut query = self.selection.select("withoutLabel");

        query = query.arg("name", name);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_mount(&self, path: String) -> Container {
        let mut query = self.selection.select("withoutMount");

        query = query.arg("path", path);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_registry_auth(&self, address: String) -> Container {
        let mut query = self.selection.select("withoutRegistryAuth");

        query = query.arg("address", address);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_unix_socket(&self, path: String) -> Container {
        let mut query = self.selection.select("withoutUnixSocket");

        query = query.arg("path", path);

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn workdir(&self) -> String {
        let mut query = self.selection.select("workdir");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Directory {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

pub struct DirectoryDockerBuildOpts {
    pub dockerfile: Option<String>,
    pub platform: Option<Platform>,
    pub build_args: Option<Vec<BuildArg>>,
    pub target: Option<String>,
}
pub struct DirectoryEntriesOpts {
    pub path: Option<String>,
}
pub struct DirectoryPipelineOpts {
    pub description: Option<String>,
}
pub struct DirectoryWithDirectoryOpts {
    pub exclude: Option<Vec<String>>,
    pub include: Option<Vec<String>>,
}
pub struct DirectoryWithFileOpts {
    pub permissions: Option<isize>,
}
pub struct DirectoryWithNewDirectoryOpts {
    pub permissions: Option<isize>,
}
pub struct DirectoryWithNewFileOpts {
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
    pub fn directory(&self, path: String) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path);

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn docker_build(&self, opts: Option<DirectoryDockerBuildOpts>) -> Container {
        let mut query = self.selection.select("dockerBuild");

        if let Some(opts) = opts {
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
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn entries(&self, opts: Option<DirectoryEntriesOpts>) -> Vec<String> {
        let mut query = self.selection.select("entries");

        if let Some(opts) = opts {
            if let Some(path) = opts.path {
                query = query.arg("path", path);
            }
        }

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn export(&self, path: String) -> bool {
        let mut query = self.selection.select("export");

        query = query.arg("path", path);

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn file(&self, path: String) -> File {
        let mut query = self.selection.select("file");

        query = query.arg("path", path);

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn id(&self) -> DirectoryId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn load_project(&self, config_path: String) -> Project {
        let mut query = self.selection.select("loadProject");

        query = query.arg("configPath", config_path);

        return Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn pipeline(&self, name: String, opts: Option<DirectoryPipelineOpts>) -> Directory {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name);
        if let Some(opts) = opts {
            if let Some(description) = opts.description {
                query = query.arg("description", description);
            }
        }

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_directory(
        &self,
        path: String,
        directory: DirectoryId,
        opts: Option<DirectoryWithDirectoryOpts>,
    ) -> Directory {
        let mut query = self.selection.select("withDirectory");

        query = query.arg("path", path);
        query = query.arg("directory", directory);
        if let Some(opts) = opts {
            if let Some(exclude) = opts.exclude {
                query = query.arg("exclude", exclude);
            }
            if let Some(include) = opts.include {
                query = query.arg("include", include);
            }
        }

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_file(
        &self,
        path: String,
        source: FileId,
        opts: Option<DirectoryWithFileOpts>,
    ) -> Directory {
        let mut query = self.selection.select("withFile");

        query = query.arg("path", path);
        query = query.arg("source", source);
        if let Some(opts) = opts {
            if let Some(permissions) = opts.permissions {
                query = query.arg("permissions", permissions);
            }
        }

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_new_directory(
        &self,
        path: String,
        opts: Option<DirectoryWithNewDirectoryOpts>,
    ) -> Directory {
        let mut query = self.selection.select("withNewDirectory");

        query = query.arg("path", path);
        if let Some(opts) = opts {
            if let Some(permissions) = opts.permissions {
                query = query.arg("permissions", permissions);
            }
        }

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn with_new_file(
        &self,
        path: String,
        contents: String,
        opts: Option<DirectoryWithNewFileOpts>,
    ) -> Directory {
        let mut query = self.selection.select("withNewFile");

        query = query.arg("path", path);
        query = query.arg("contents", contents);
        if let Some(opts) = opts {
            if let Some(permissions) = opts.permissions {
                query = query.arg("permissions", permissions);
            }
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
    pub fn without_directory(&self, path: String) -> Directory {
        let mut query = self.selection.select("withoutDirectory");

        query = query.arg("path", path);

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn without_file(&self, path: String) -> Directory {
        let mut query = self.selection.select("withoutFile");

        query = query.arg("path", path);

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
    pub fn name(&self) -> String {
        let mut query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn value(&self) -> String {
        let mut query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct File {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl File {
    pub fn contents(&self) -> String {
        let mut query = self.selection.select("contents");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn export(&self, path: String) -> bool {
        let mut query = self.selection.select("export");

        query = query.arg("path", path);

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn id(&self) -> FileId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn secret(&self) -> Secret {
        let mut query = self.selection.select("secret");

        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn size(&self) -> isize {
        let mut query = self.selection.select("size");

        query.execute(&graphql_client(&self.conn)).unwrap()
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

pub struct GitRefTreeOpts {
    pub ssh_known_hosts: Option<String>,
    pub ssh_auth_socket: Option<SocketId>,
}

impl GitRef {
    pub fn digest(&self) -> String {
        let mut query = self.selection.select("digest");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn tree(&self, opts: Option<GitRefTreeOpts>) -> Directory {
        let mut query = self.selection.select("tree");

        if let Some(opts) = opts {
            if let Some(ssh_known_hosts) = opts.ssh_known_hosts {
                query = query.arg("sshKnownHosts", ssh_known_hosts);
            }
            if let Some(ssh_auth_socket) = opts.ssh_auth_socket {
                query = query.arg("sshAuthSocket", ssh_auth_socket);
            }
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
    pub fn branch(&self, name: String) -> GitRef {
        let mut query = self.selection.select("branch");

        query = query.arg("name", name);

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn branches(&self) -> Vec<String> {
        let mut query = self.selection.select("branches");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn commit(&self, id: String) -> GitRef {
        let mut query = self.selection.select("commit");

        query = query.arg("id", id);

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn tag(&self, name: String) -> GitRef {
        let mut query = self.selection.select("tag");

        query = query.arg("name", name);

        return GitRef {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn tags(&self) -> Vec<String> {
        let mut query = self.selection.select("tags");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Host {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

pub struct HostDirectoryOpts {
    pub exclude: Option<Vec<String>>,
    pub include: Option<Vec<String>>,
}
pub struct HostWorkdirOpts {
    pub exclude: Option<Vec<String>>,
    pub include: Option<Vec<String>>,
}

impl Host {
    pub fn directory(&self, path: String, opts: Option<HostDirectoryOpts>) -> Directory {
        let mut query = self.selection.select("directory");

        query = query.arg("path", path);
        if let Some(opts) = opts {
            if let Some(exclude) = opts.exclude {
                query = query.arg("exclude", exclude);
            }
            if let Some(include) = opts.include {
                query = query.arg("include", include);
            }
        }

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn env_variable(&self, name: String) -> HostVariable {
        let mut query = self.selection.select("envVariable");

        query = query.arg("name", name);

        return HostVariable {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn unix_socket(&self, path: String) -> Socket {
        let mut query = self.selection.select("unixSocket");

        query = query.arg("path", path);

        return Socket {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn workdir(&self, opts: Option<HostWorkdirOpts>) -> Directory {
        let mut query = self.selection.select("workdir");

        if let Some(opts) = opts {
            if let Some(exclude) = opts.exclude {
                query = query.arg("exclude", exclude);
            }
            if let Some(include) = opts.include {
                query = query.arg("include", include);
            }
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
        let mut query = self.selection.select("secret");

        return Secret {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn value(&self) -> String {
        let mut query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Label {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Label {
    pub fn name(&self) -> String {
        let mut query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn value(&self) -> String {
        let mut query = self.selection.select("value");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Project {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Project {
    pub fn extensions(&self) -> Vec<Project> {
        let mut query = self.selection.select("extensions");

        return vec![Project {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        }];
    }
    pub fn generated_code(&self) -> Directory {
        let mut query = self.selection.select("generatedCode");

        return Directory {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn install(&self) -> bool {
        let mut query = self.selection.select("install");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn name(&self) -> String {
        let mut query = self.selection.select("name");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn schema(&self) -> String {
        let mut query = self.selection.select("schema");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn sdk(&self) -> String {
        let mut query = self.selection.select("sdk");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Query {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

pub struct QueryContainerOpts {
    pub id: Option<ContainerId>,
    pub platform: Option<Platform>,
}
pub struct QueryDirectoryOpts {
    pub id: Option<DirectoryId>,
}
pub struct QueryGitOpts {
    pub keep_git_dir: Option<bool>,
}
pub struct QueryPipelineOpts {
    pub description: Option<String>,
}
pub struct QuerySocketOpts {
    pub id: Option<SocketId>,
}

impl Query {
    pub fn cache_volume(&self, key: String) -> CacheVolume {
        let mut query = self.selection.select("cacheVolume");

        query = query.arg("key", key);

        return CacheVolume {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn container(&self, opts: Option<QueryContainerOpts>) -> Container {
        let mut query = self.selection.select("container");

        if let Some(opts) = opts {
            if let Some(id) = opts.id {
                query = query.arg("id", id);
            }
            if let Some(platform) = opts.platform {
                query = query.arg("platform", platform);
            }
        }

        return Container {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn default_platform(&self) -> Platform {
        let mut query = self.selection.select("defaultPlatform");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn directory(&self, opts: Option<QueryDirectoryOpts>) -> Directory {
        let mut query = self.selection.select("directory");

        if let Some(opts) = opts {
            if let Some(id) = opts.id {
                query = query.arg("id", id);
            }
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
    pub fn git(&self, url: String, opts: Option<QueryGitOpts>) -> GitRepository {
        let mut query = self.selection.select("git");

        query = query.arg("url", url);
        if let Some(opts) = opts {
            if let Some(keep_git_dir) = opts.keep_git_dir {
                query = query.arg("keepGitDir", keep_git_dir);
            }
        }

        return GitRepository {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn host(&self) -> Host {
        let mut query = self.selection.select("host");

        return Host {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn http(&self, url: String) -> File {
        let mut query = self.selection.select("http");

        query = query.arg("url", url);

        return File {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn pipeline(&self, name: String, opts: Option<QueryPipelineOpts>) -> Query {
        let mut query = self.selection.select("pipeline");

        query = query.arg("name", name);
        if let Some(opts) = opts {
            if let Some(description) = opts.description {
                query = query.arg("description", description);
            }
        }

        return Query {
            proc: self.proc.clone(),
            selection: query,
            conn: self.conn.clone(),
        };
    }
    pub fn project(&self, name: String) -> Project {
        let mut query = self.selection.select("project");

        query = query.arg("name", name);

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
    pub fn socket(&self, opts: Option<QuerySocketOpts>) -> Socket {
        let mut query = self.selection.select("socket");

        if let Some(opts) = opts {
            if let Some(id) = opts.id {
                query = query.arg("id", id);
            }
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
    pub fn id(&self) -> SecretId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
    pub fn plaintext(&self) -> String {
        let mut query = self.selection.select("plaintext");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
pub struct Socket {
    pub proc: Arc<Child>,
    pub selection: Selection,
    pub conn: ConnectParams,
}

impl Socket {
    pub fn id(&self) -> SocketId {
        let mut query = self.selection.select("id");

        query.execute(&graphql_client(&self.conn)).unwrap()
    }
}
#[derive(Serialize)]
pub enum CacheSharingMode {
    SHARED,
    PRIVATE,
    LOCKED,
}
