//! Generated bindings owned by the GraphQL `Container` type.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#[doc = "An OCI-compatible container, also known as a Docker container."]
#[derive(Clone)]
pub struct Container {
    pub(crate) session: crate::lifecycle::SessionHandle,
    pub(crate) selection: crate::query::Selection,
}
#[doc = "Owned optional arguments for GraphQL operation `Container.asService`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerAsServiceOpts {
    #[doc = "Command to run instead of the container's default command (e.g., \\[\"go\", \"run\", \"main.go\"\\]).\n\nIf empty, the container's default command is used.\n\n`None` omits GraphQL Wire_Name `args` and preserves engine default `List(\\[\\])`."]
    pub args: Option<Vec<String>>,
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the args according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
    #[doc = "If set, skip the automatic init process injected into containers by default.\n\nThis should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.\n\n`None` omits GraphQL Wire_Name `noInit` and preserves engine default `Boolean(false)`."]
    pub no_init: Option<bool>,
    #[doc = "If the container has an entrypoint, prepend it to the args.\n\n`None` omits GraphQL Wire_Name `useEntrypoint` and preserves engine default `Boolean(false)`."]
    pub use_entrypoint: Option<bool>,
}
impl ContainerAsServiceOpts {
    #[doc = "Sets GraphQL argument `args` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_args(mut self, value: Vec<impl Into<String>>) -> Self {
        self.args = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `noInit` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_init(mut self, value: bool) -> Self {
        self.no_init = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `useEntrypoint` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_use_entrypoint(mut self, value: bool) -> Self {
        self.use_entrypoint = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.asTarball`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerAsTarballOpts {
    #[doc = "Force each layer of the image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Use the specified media types for the image's layers.\n\nDefaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
    #[doc = "Identifiers for other platform specific containers.\n\nUsed for multi-platform images.\n\n`None` omits GraphQL Wire_Name `platformVariants` and preserves engine default `List(\\[\\])`."]
    pub platform_variants: Option<Vec<crate::IdInput<super::Container>>>,
}
impl ContainerAsTarballOpts {
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `platformVariants` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform_variants(mut self, value: Vec<crate::IdInput<super::Container>>) -> Self {
        self.platform_variants = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.directory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerDirectoryOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerDirectoryOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.exists`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerExistsOpts {
    #[doc = "If specified, do not follow symlinks.\n\n`None` omits GraphQL Wire_Name `doNotFollowSymlinks` and preserves engine default `Boolean(false)`."]
    pub do_not_follow_symlinks: Option<bool>,
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "If specified, also validate the type of file (e.g. \"REGULAR_TYPE\", \"DIRECTORY_TYPE\", or \"SYMLINK_TYPE\").\n\n`None` omits GraphQL Wire_Name `expectedType`."]
    pub expected_type: Option<super::ExistsType>,
}
impl ContainerExistsOpts {
    #[doc = "Sets GraphQL argument `doNotFollowSymlinks` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_do_not_follow_symlinks(mut self, value: bool) -> Self {
        self.do_not_follow_symlinks = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `expectedType` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expected_type(mut self, value: super::ExistsType) -> Self {
        self.expected_type = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.export`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerExportOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Force each layer of the exported image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Use the specified media types for the exported image's layers.\n\nDefaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
    #[doc = "Identifiers for other platform specific containers.\n\nUsed for multi-platform image.\n\n`None` omits GraphQL Wire_Name `platformVariants` and preserves engine default `List(\\[\\])`."]
    pub platform_variants: Option<Vec<crate::IdInput<super::Container>>>,
}
impl ContainerExportOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `platformVariants` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform_variants(mut self, value: Vec<crate::IdInput<super::Container>>) -> Self {
        self.platform_variants = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.exportImage`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerExportImageOpts {
    #[doc = "Force each layer of the exported image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Use the specified media types for the exported image's layers.\n\nDefaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
    #[doc = "Identifiers for other platform specific containers.\n\nUsed for multi-platform image.\n\n`None` omits GraphQL Wire_Name `platformVariants` and preserves engine default `List(\\[\\])`."]
    pub platform_variants: Option<Vec<crate::IdInput<super::Container>>>,
}
impl ContainerExportImageOpts {
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `platformVariants` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform_variants(mut self, value: Vec<crate::IdInput<super::Container>>) -> Self {
        self.platform_variants = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.file`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.from`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerFromOpts {
    #[doc = "Allow HTTPS registry communication without verifying the server certificate.\n\n`None` omits GraphQL Wire_Name `insecureSkipTLSVerify` and preserves engine default `Boolean(false)`."]
    pub insecure_skip_tls_verify: Option<bool>,
    #[doc = "Protocol to use for registry communication.\n\nDefaults to \"HTTPS\". Use \"HTTP\" only for plain HTTP registries.\n\n`None` omits GraphQL Wire_Name `protocol`."]
    pub protocol: Option<super::RegistryProtocol>,
    #[doc = "Service to use as the registry endpoint for the image address.\n\nThe service will be started only for this pull.\n\n`None` omits GraphQL Wire_Name `registryService`."]
    pub registry_service: Option<crate::IdInput<super::Service>>,
}
impl ContainerFromOpts {
    #[doc = "Sets GraphQL argument `insecureSkipTLSVerify` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_skip_tls_verify(mut self, value: bool) -> Self {
        self.insecure_skip_tls_verify = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `protocol` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_protocol(mut self, value: super::RegistryProtocol) -> Self {
        self.protocol = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `registryService` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_registry_service(mut self, value: crate::IdInput<super::Service>) -> Self {
        self.registry_service = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.import`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerImportOpts {
    #[doc = "Identifies the tag to import from the archive, if the archive bundles multiple tags.\n\n`None` omits GraphQL Wire_Name `tag` and preserves engine default `String(\"\")`."]
    pub tag: Option<String>,
}
impl ContainerImportOpts {
    #[doc = "Sets GraphQL argument `tag` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_tag(mut self, value: impl Into<String>) -> Self {
        self.tag = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.layer`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerLayerOpts {
    #[doc = "Force each layer of the image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Media types to use for image layers. Defaults to OCI.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
}
impl ContainerLayerOpts {
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.manifest`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerManifestOpts {
    #[doc = "Force each layer of the image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Media types to use for image layers. Defaults to OCI.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
}
impl ContainerManifestOpts {
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.publish`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerPublishOpts {
    #[doc = "Force each layer of the published image to use the specified compression algorithm.\n\nIf this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.\n\n`None` omits GraphQL Wire_Name `forcedCompression`."]
    pub forced_compression: Option<super::ImageLayerCompression>,
    #[doc = "Allow HTTPS registry communication without verifying the server certificate.\n\n`None` omits GraphQL Wire_Name `insecureSkipTLSVerify` and preserves engine default `Boolean(false)`."]
    pub insecure_skip_tls_verify: Option<bool>,
    #[doc = "Use the specified media types for the published image's layers.\n\nDefaults to \"OCI\", which is compatible with most recent registries, but \"Docker\" may be needed for older registries without OCI support.\n\n`None` omits GraphQL Wire_Name `mediaTypes` and preserves engine default `Enum(SchemaName(\"OCIMediaTypes\"))`."]
    pub media_types: Option<super::ImageMediaTypes>,
    #[doc = "Identifiers for other platform specific containers.\n\nUsed for multi-platform image.\n\n`None` omits GraphQL Wire_Name `platformVariants` and preserves engine default `List(\\[\\])`."]
    pub platform_variants: Option<Vec<crate::IdInput<super::Container>>>,
    #[doc = "Protocol to use for registry communication.\n\nDefaults to \"HTTPS\". Use \"HTTP\" only for plain HTTP registries.\n\n`None` omits GraphQL Wire_Name `protocol`."]
    pub protocol: Option<super::RegistryProtocol>,
    #[doc = "Service to use as the registry endpoint for the image address.\n\nThe service will be started only for this push.\n\n`None` omits GraphQL Wire_Name `registryService`."]
    pub registry_service: Option<crate::IdInput<super::Service>>,
}
impl ContainerPublishOpts {
    #[doc = "Sets GraphQL argument `forcedCompression` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_forced_compression(mut self, value: super::ImageLayerCompression) -> Self {
        self.forced_compression = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureSkipTLSVerify` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_skip_tls_verify(mut self, value: bool) -> Self {
        self.insecure_skip_tls_verify = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mediaTypes` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_media_types(mut self, value: super::ImageMediaTypes) -> Self {
        self.media_types = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `platformVariants` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_platform_variants(mut self, value: Vec<crate::IdInput<super::Container>>) -> Self {
        self.platform_variants = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `protocol` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_protocol(mut self, value: super::RegistryProtocol) -> Self {
        self.protocol = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `registryService` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_registry_service(mut self, value: crate::IdInput<super::Service>) -> Self {
        self.registry_service = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.stat`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerStatOpts {
    #[doc = "If specified, do not follow symlinks.\n\n`None` omits GraphQL Wire_Name `doNotFollowSymlinks` and preserves engine default `Boolean(false)`."]
    pub do_not_follow_symlinks: Option<bool>,
}
impl ContainerStatOpts {
    #[doc = "Sets GraphQL argument `doNotFollowSymlinks` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_do_not_follow_symlinks(mut self, value: bool) -> Self {
        self.do_not_follow_symlinks = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.terminal`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerTerminalOpts {
    #[doc = "If set, override the container's default terminal command and invoke these command arguments instead.\n\n`None` omits GraphQL Wire_Name `cmd` and preserves engine default `List(\\[\\])`."]
    pub cmd: Option<Vec<String>>,
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
}
impl ContainerTerminalOpts {
    #[doc = "Sets GraphQL argument `cmd` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_cmd(mut self, value: Vec<impl Into<String>>) -> Self {
        self.cmd = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.up`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerUpOpts {
    #[doc = "Command to run instead of the container's default command (e.g., \\[\"go\", \"run\", \"main.go\"\\]).\n\nIf empty, the container's default command is used.\n\n`None` omits GraphQL Wire_Name `args` and preserves engine default `List(\\[\\])`."]
    pub args: Option<Vec<String>>,
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the args according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
    #[doc = "If set, skip the automatic init process injected into containers by default.\n\nThis should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.\n\n`None` omits GraphQL Wire_Name `noInit` and preserves engine default `Boolean(false)`."]
    pub no_init: Option<bool>,
    #[doc = "List of frontend/backend port mappings to forward.\n\nFrontend is the port accepting traffic on the host, backend is the service port.\n\n`None` omits GraphQL Wire_Name `ports` and preserves engine default `List(\\[\\])`."]
    pub ports: Option<Vec<super::PortForward>>,
    #[doc = "Bind each tunnel port to a random port on the host.\n\n`None` omits GraphQL Wire_Name `random` and preserves engine default `Boolean(false)`."]
    pub random: Option<bool>,
    #[doc = "If the container has an entrypoint, prepend it to the args.\n\n`None` omits GraphQL Wire_Name `useEntrypoint` and preserves engine default `Boolean(false)`."]
    pub use_entrypoint: Option<bool>,
}
impl ContainerUpOpts {
    #[doc = "Sets GraphQL argument `args` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_args(mut self, value: Vec<impl Into<String>>) -> Self {
        self.args = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `noInit` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_init(mut self, value: bool) -> Self {
        self.no_init = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `ports` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_ports(mut self, value: Vec<super::PortForward>) -> Self {
        self.ports = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `random` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_random(mut self, value: bool) -> Self {
        self.random = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `useEntrypoint` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_use_entrypoint(mut self, value: bool) -> Self {
        self.use_entrypoint = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withDefaultTerminalCmd`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithDefaultTerminalCmdOpts {
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. This is similar to running a command with \"sudo\" or executing \"docker run\" with the \"--privileged\" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
}
impl ContainerWithDefaultTerminalCmdOpts {
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withDirectory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithDirectoryOpts {
    #[doc = "Patterns to exclude in the written directory (e.g. \\[\"node_modules/**\", \".gitignore\", \".git/\"\\]).\n\n`None` omits GraphQL Wire_Name `exclude` and preserves engine default `List(\\[\\])`."]
    pub exclude: Option<Vec<String>>,
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Apply .gitignore rules when writing the directory.\n\n`None` omits GraphQL Wire_Name `gitignore` and preserves engine default `Boolean(false)`."]
    pub gitignore: Option<bool>,
    #[doc = "Patterns to include in the written directory (e.g. \\[\"*.go\", \"go.mod\", \"go.sum\"\\]).\n\n`None` omits GraphQL Wire_Name `include` and preserves engine default `List(\\[\\])`."]
    pub include: Option<Vec<String>>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl ContainerWithDirectoryOpts {
    #[doc = "Sets GraphQL argument `exclude` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_exclude(mut self, value: Vec<impl Into<String>>) -> Self {
        self.exclude = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `gitignore` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_gitignore(mut self, value: bool) -> Self {
        self.gitignore = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `include` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_include(mut self, value: Vec<impl Into<String>>) -> Self {
        self.include = Some(value.into_iter().map(Into::into).collect());
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withDockerHealthcheck`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithDockerHealthcheckOpts {
    #[doc = "Interval between running healthcheck. Example: \"30s\"\n\n`None` omits GraphQL Wire_Name `interval`."]
    pub interval: Option<String>,
    #[doc = "The maximum number of consecutive failures before the container is marked as unhealthy. Example: \"3\"\n\n`None` omits GraphQL Wire_Name `retries`."]
    pub retries: Option<i64>,
    #[doc = "When true, command must be a single element, which is run using the container's shell\n\n`None` omits GraphQL Wire_Name `shell`."]
    pub shell: Option<bool>,
    #[doc = "StartInterval configures the duration between checks during the startup phase. Example: \"5s\"\n\n`None` omits GraphQL Wire_Name `startInterval`."]
    pub start_interval: Option<String>,
    #[doc = "StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example: \"0s\"\n\n`None` omits GraphQL Wire_Name `startPeriod`."]
    pub start_period: Option<String>,
    #[doc = "Healthcheck timeout. Example: \"3s\"\n\n`None` omits GraphQL Wire_Name `timeout`."]
    pub timeout: Option<String>,
}
impl ContainerWithDockerHealthcheckOpts {
    #[doc = "Sets GraphQL argument `interval` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_interval(mut self, value: impl Into<String>) -> Self {
        self.interval = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `retries` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_retries(mut self, value: i64) -> Self {
        self.retries = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `shell` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_shell(mut self, value: bool) -> Self {
        self.shell = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `startInterval` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_start_interval(mut self, value: impl Into<String>) -> Self {
        self.start_interval = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `startPeriod` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_start_period(mut self, value: impl Into<String>) -> Self {
        self.start_period = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `timeout` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_timeout(mut self, value: impl Into<String>) -> Self {
        self.timeout = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withEntrypoint`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithEntrypointOpts {
    #[doc = "Don't reset the default arguments when setting the entrypoint. By default it is reset, since entrypoint and default args are often tightly coupled.\n\n`None` omits GraphQL Wire_Name `keepDefaultArgs` and preserves engine default `Boolean(false)`."]
    pub keep_default_args: Option<bool>,
}
impl ContainerWithEntrypointOpts {
    #[doc = "Sets GraphQL argument `keepDefaultArgs` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_keep_default_args(mut self, value: bool) -> Self {
        self.keep_default_args = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withEnvVariable`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithEnvVariableOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value according to the current environment variables defined in the container (e.g. \"/opt/bin:$PATH\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithEnvVariableOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withExec`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithExecOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the args according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Exit codes this command is allowed to exit with without error\n\n`None` omits GraphQL Wire_Name `expect` and preserves engine default `Enum(SchemaName(\"SUCCESS\"))`."]
    pub expect: Option<super::ReturnType>,
    #[doc = "Provides Dagger access to the executed command.\n\n`None` omits GraphQL Wire_Name `experimentalPrivilegedNesting` and preserves engine default `Boolean(false)`."]
    pub experimental_privileged_nesting: Option<bool>,
    #[doc = "Execute the command with all root capabilities. Like --privileged in Docker\n\nDANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.\n\n`None` omits GraphQL Wire_Name `insecureRootCapabilities` and preserves engine default `Boolean(false)`."]
    pub insecure_root_capabilities: Option<bool>,
    #[doc = "Skip the automatic init process injected into containers by default.\n\nOnly use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.\n\n`None` omits GraphQL Wire_Name `noInit` and preserves engine default `Boolean(false)`."]
    pub no_init: Option<bool>,
    #[doc = "Redirect the command's standard error to a file in the container. Example: \"./stderr.txt\"\n\n`None` omits GraphQL Wire_Name `redirectStderr` and preserves engine default `String(\"\")`."]
    pub redirect_stderr: Option<String>,
    #[doc = "Redirect the command's standard input from a file in the container. Example: \"./stdin.txt\"\n\n`None` omits GraphQL Wire_Name `redirectStdin` and preserves engine default `String(\"\")`."]
    pub redirect_stdin: Option<String>,
    #[doc = "Redirect the command's standard output to a file in the container. Example: \"./stdout.txt\"\n\n`None` omits GraphQL Wire_Name `redirectStdout` and preserves engine default `String(\"\")`."]
    pub redirect_stdout: Option<String>,
    #[doc = "Content to write to the command's standard input. Example: \"Hello world\")\n\n`None` omits GraphQL Wire_Name `stdin` and preserves engine default `String(\"\")`."]
    pub stdin: Option<String>,
    #[doc = "Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.\n\n`None` omits GraphQL Wire_Name `useEntrypoint` and preserves engine default `Boolean(false)`."]
    pub use_entrypoint: Option<bool>,
}
impl ContainerWithExecOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `expect` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expect(mut self, value: super::ReturnType) -> Self {
        self.expect = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `experimentalPrivilegedNesting` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_privileged_nesting(mut self, value: bool) -> Self {
        self.experimental_privileged_nesting = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `insecureRootCapabilities` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_insecure_root_capabilities(mut self, value: bool) -> Self {
        self.insecure_root_capabilities = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `noInit` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_no_init(mut self, value: bool) -> Self {
        self.no_init = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `redirectStderr` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_redirect_stderr(mut self, value: impl Into<String>) -> Self {
        self.redirect_stderr = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `redirectStdin` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_redirect_stdin(mut self, value: impl Into<String>) -> Self {
        self.redirect_stdin = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `redirectStdout` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_redirect_stdout(mut self, value: impl Into<String>) -> Self {
        self.redirect_stdout = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `stdin` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_stdin(mut self, value: impl Into<String>) -> Self {
        self.stdin = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `useEntrypoint` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_use_entrypoint(mut self, value: bool) -> Self {
        self.use_entrypoint = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withExposedPort`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithExposedPortOpts {
    #[doc = "Port description. Example: \"payment API endpoint\"\n\n`None` omits GraphQL Wire_Name `description`."]
    pub description: Option<String>,
    #[doc = "Skip the health check when run as a service.\n\n`None` omits GraphQL Wire_Name `experimentalSkipHealthcheck` and preserves engine default `Boolean(false)`."]
    pub experimental_skip_healthcheck: Option<bool>,
    #[doc = "Network protocol. Example: \"tcp\"\n\n`None` omits GraphQL Wire_Name `protocol` and preserves engine default `Enum(SchemaName(\"TCP\"))`."]
    pub protocol: Option<super::NetworkProtocol>,
}
impl ContainerWithExposedPortOpts {
    #[doc = "Sets GraphQL argument `description` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_description(mut self, value: impl Into<String>) -> Self {
        self.description = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `experimentalSkipHealthcheck` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_experimental_skip_healthcheck(mut self, value: bool) -> Self {
        self.experimental_skip_healthcheck = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `protocol` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_protocol(mut self, value: super::NetworkProtocol) -> Self {
        self.protocol = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Permissions of the new file. Example: 0600\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl ContainerWithFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withFiles`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithFilesOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the files.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Permission given to the copied files (e.g., 0600).\n\n`None` omits GraphQL Wire_Name `permissions`."]
    pub permissions: Option<i64>,
}
impl ContainerWithFilesOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedCache`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedCacheOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the mounted cache directory.\n\nNote that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Sharing mode of the cache volume.\n\n`None` omits GraphQL Wire_Name `sharing` and preserves engine default `Enum(SchemaName(\"SHARED\"))`."]
    pub sharing: Option<super::CacheSharingMode>,
    #[doc = "Identifier of the directory to use as the cache volume's root.\n\n`None` omits GraphQL Wire_Name `source`."]
    pub source: Option<crate::IdInput<super::Directory>>,
}
impl ContainerWithMountedCacheOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `sharing` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_sharing(mut self, value: super::CacheSharingMode) -> Self {
        self.sharing = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `source` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_source(mut self, value: crate::IdInput<super::Directory>) -> Self {
        self.source = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedDirectory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedDirectoryOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the mounted directory and its contents.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Mount the directory read-only.\n\n`None` omits GraphQL Wire_Name `readOnly` and preserves engine default `Boolean(false)`."]
    pub read_only: Option<bool>,
}
impl ContainerWithMountedDirectoryOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `readOnly` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_read_only(mut self, value: bool) -> Self {
        self.read_only = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user or user:group to set for the mounted file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
}
impl ContainerWithMountedFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedSecret`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedSecretOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "Permission given to the mounted secret (e.g., 0600).\n\nThis option requires an owner to be set to be active.\n\n`None` omits GraphQL Wire_Name `mode` and preserves engine default `Int(256)`."]
    pub mode: Option<i64>,
    #[doc = "A user:group to set for the mounted secret.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
}
impl ContainerWithMountedSecretOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `mode` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_mode(mut self, value: i64) -> Self {
        self.mode = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedTemp`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedTempOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Size of the temporary directory in bytes.\n\n`None` omits GraphQL Wire_Name `size`."]
    pub size: Option<i64>,
}
impl ContainerWithMountedTempOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `size` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_size(mut self, value: i64) -> Self {
        self.size = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withMountedVolume`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithMountedVolumeOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Mount the volume read-only.\n\n`None` omits GraphQL Wire_Name `readOnly` and preserves engine default `Boolean(false)`."]
    pub read_only: Option<bool>,
}
impl ContainerWithMountedVolumeOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `readOnly` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_read_only(mut self, value: bool) -> Self {
        self.read_only = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withNewFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithNewFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the file.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
    #[doc = "Permissions of the new file. Example: 0600\n\n`None` omits GraphQL Wire_Name `permissions` and preserves engine default `Int(420)`."]
    pub permissions: Option<i64>,
}
impl ContainerWithNewFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
    #[doc = "Sets GraphQL argument `permissions` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_permissions(mut self, value: i64) -> Self {
        self.permissions = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withSymlink`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithSymlinkOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithSymlinkOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withUnixSocket`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithUnixSocketOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
    #[doc = "Set the owner to the container's current user.\n\n`None` omits GraphQL Wire_Name `inheritOwner` and preserves engine default `Boolean(false)`."]
    pub inherit_owner: Option<bool>,
    #[doc = "A user:group to set for the mounted socket.\n\nThe user and group can either be an ID (1000:1000) or a name (foo:bar).\n\nIf the group is omitted, it defaults to the same as the user.\n\n`None` omits GraphQL Wire_Name `owner` and preserves engine default `String(\"\")`."]
    pub owner: Option<String>,
}
impl ContainerWithUnixSocketOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `inheritOwner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_inherit_owner(mut self, value: bool) -> Self {
        self.inherit_owner = Some(value);
        self
    }
    #[doc = "Sets GraphQL argument `owner` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_owner(mut self, value: impl Into<String>) -> Self {
        self.owner = Some(value.into());
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withWorkdir`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithWorkdirOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithWorkdirOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutDirectory`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutDirectoryOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithoutDirectoryOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutEntrypoint`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutEntrypointOpts {
    #[doc = "Don't remove the default arguments when unsetting the entrypoint.\n\n`None` omits GraphQL Wire_Name `keepDefaultArgs` and preserves engine default `Boolean(false)`."]
    pub keep_default_args: Option<bool>,
}
impl ContainerWithoutEntrypointOpts {
    #[doc = "Sets GraphQL argument `keepDefaultArgs` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_keep_default_args(mut self, value: bool) -> Self {
        self.keep_default_args = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutExposedPort`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutExposedPortOpts {
    #[doc = "Port protocol to unexpose\n\n`None` omits GraphQL Wire_Name `protocol` and preserves engine default `Enum(SchemaName(\"TCP\"))`."]
    pub protocol: Option<super::NetworkProtocol>,
}
impl ContainerWithoutExposedPortOpts {
    #[doc = "Sets GraphQL argument `protocol` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_protocol(mut self, value: super::NetworkProtocol) -> Self {
        self.protocol = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutFile`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutFileOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithoutFileOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutFiles`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutFilesOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of paths according to the current environment variables defined in the container (e.g. \"/$VAR/foo.txt\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithoutFilesOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutMount`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutMountOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithoutMountOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
#[doc = "Owned optional arguments for GraphQL operation `Container.withoutUnixSocket`; reuse does not mutate caller state."]
#[derive(Clone, Debug, Default)]
#[non_exhaustive]
pub struct ContainerWithoutUnixSocketOpts {
    #[doc = "Replace \"${VAR}\" or \"$VAR\" in the value of path according to the current environment variables defined in the container (e.g. \"/$VAR/foo\").\n\n`None` omits GraphQL Wire_Name `expand` and preserves engine default `Boolean(false)`."]
    pub expand: Option<bool>,
}
impl ContainerWithoutUnixSocketOpts {
    #[doc = "Sets GraphQL argument `expand` to a concrete value instead of omitting it."]
    #[must_use]
    pub fn with_expand(mut self, value: bool) -> Self {
        self.expand = Some(value);
        self
    }
}
impl crate::IntoID<crate::Id> for Container {
    fn into_id(
        self,
    ) -> core::pin::Pin<
        Box<dyn core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send>,
    > {
        Box::pin(async move { self.id().await })
    }
}
impl crate::loadable::private::Sealed for Container {
    fn graphql_type() -> &'static str {
        "Container"
    }
    fn from_query(
        session: crate::lifecycle::SessionHandle,
        selection: crate::query::Selection,
    ) -> Self {
        Self { session, selection }
    }
}
impl From<Container> for crate::IdInput<Container> {
    fn from(value: Container) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Container> for crate::IdInput<super::ExportableClient> {
    fn from(value: Container) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Container> for crate::IdInput<super::NodeClient> {
    fn from(value: Container) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl From<Container> for crate::IdInput<super::SyncerClient> {
    fn from(value: Container) -> Self {
        crate::IdInput::lazy(value)
    }
}
impl Container {
    #[doc = "Turn the container into a Service.\n\nBe sure to set any exposed ports before this conversion.\n\nSelects GraphQL Wire_Name `asService` on `Container`."]
    #[must_use]
    pub fn as_service(&self) -> super::Service {
        let query = self.selection.select("asService");
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asService` with a borrowed, reusable `ContainerAsServiceOpts` value."]
    #[must_use]
    pub fn as_service_opts(&self, opts: &ContainerAsServiceOpts) -> super::Service {
        let query = self.selection.select("asService");
        let query = if let Some(value) = &opts.args {
            query.arg("args", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_init {
            query.arg("noInit", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.use_entrypoint {
            query.arg("useEntrypoint", value)
        } else {
            query
        };
        super::Service {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Package the container state as an OCI image, and return it as a tar archive\n\nSelects GraphQL Wire_Name `asTarball` on `Container`."]
    #[must_use]
    pub fn as_tarball(&self) -> super::File {
        let query = self.selection.select("asTarball");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `asTarball` with a borrowed, reusable `ContainerAsTarballOpts` value."]
    #[must_use]
    pub fn as_tarball_opts(&self, opts: &ContainerAsTarballOpts) -> super::File {
        let query = self.selection.select("asTarball");
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.platform_variants {
            query.arg_id_input("platformVariants", value.clone())
        } else {
            query
        };
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "The combined buffered standard output and standard error stream of the last executed command\n\nReturns an error if no command was executed\n\nSelects GraphQL Wire_Name `combinedOutput` on `Container`."]
    pub async fn combined_output(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("combinedOutput");
        query.execute(&self.session).await
    }
    #[doc = "Return the container's default arguments.\n\nSelects GraphQL Wire_Name `defaultArgs` on `Container`."]
    pub async fn default_args(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("defaultArgs");
        query.execute(&self.session).await
    }
    #[doc = "Retrieve a directory from the container's root filesystem\n\nMounts are included.\n\nSelects GraphQL Wire_Name `directory` on `Container`."]
    #[must_use]
    pub fn directory(&self, path: impl Into<String>) -> super::Directory {
        let query = self.selection.select("directory");
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `directory` with a borrowed, reusable `ContainerDirectoryOpts` value."]
    #[must_use]
    pub fn directory_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerDirectoryOpts,
    ) -> super::Directory {
        let query = self.selection.select("directory");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container's configured docker healthcheck.\n\nSelects GraphQL Wire_Name `dockerHealthcheck` on `Container`."]
    pub async fn docker_healthcheck(
        &self,
    ) -> Result<Option<super::HealthcheckConfig>, crate::QueryError> {
        let query = self.selection.select("dockerHealthcheck");
        let query = query.select("id");
        query
            .execute_reentry::<super::HealthcheckConfig, Option<crate::Id>>(
                &self.session,
                "HealthcheckConfig",
            )
            .await
    }
    #[doc = "Return the container's OCI entrypoint.\n\nSelects GraphQL Wire_Name `entrypoint` on `Container`."]
    pub async fn entrypoint(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("entrypoint");
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the value of the specified persistent environment variable.\n\nSelects GraphQL Wire_Name `envVariable` on `Container`."]
    pub async fn env_variable(
        &self,
        name: impl Into<String>,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("envVariable");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the list of persistent environment variables configured on the container.\n\nSelects GraphQL Wire_Name `envVariables` on `Container`."]
    pub async fn env_variables(&self) -> Result<Vec<super::EnvVariable>, crate::QueryError> {
        let query = self.selection.select("envVariables");
        let query = query.select("id");
        query
            .execute_reentry::<super::EnvVariable, Vec<crate::Id>>(&self.session, "EnvVariable")
            .await
    }
    #[doc = "check if a file or directory exists\n\nSelects GraphQL Wire_Name `exists` on `Container`."]
    pub async fn exists(&self, path: impl Into<String>) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("exists");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `exists` with a borrowed, reusable `ContainerExistsOpts` value."]
    pub async fn exists_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerExistsOpts,
    ) -> Result<bool, crate::QueryError> {
        let query = self.selection.select("exists");
        let query = if let Some(value) = &opts.do_not_follow_symlinks {
            query.arg("doNotFollowSymlinks", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expected_type {
            query.arg("expectedType", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "The exit code of the last executed command\n\nReturns an error if no command was executed\n\nSelects GraphQL Wire_Name `exitCode` on `Container`."]
    pub async fn exit_code(&self) -> Result<i64, crate::QueryError> {
        let query = self.selection.select("exitCode");
        query.execute(&self.session).await
    }
    #[doc = "EXPERIMENTAL API! Subject to change/removal at any time.\n\nConfigures all available GPUs on the host to be accessible to this container.\n\nThis currently works for Nvidia devices only.\n\nSelects GraphQL Wire_Name `experimentalWithAllGPUs` on `Container`."]
    #[must_use]
    pub fn experimental_with_all_gp_us(&self) -> super::Container {
        let query = self.selection.select("experimentalWithAllGPUs");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "EXPERIMENTAL API! Subject to change/removal at any time.\n\nConfigures the provided list of devices to be accessible to this container.\n\nThis currently works for Nvidia devices only.\n\nSelects GraphQL Wire_Name `experimentalWithGPU` on `Container`."]
    #[must_use]
    pub fn experimental_with_gpu(&self, devices: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("experimentalWithGPU");
        let devices = devices.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("devices", devices);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Writes the container as an OCI tarball to the destination file path on the host.\n\nIt can also export platform variants.\n\nSelects GraphQL Wire_Name `export` on `Container`."]
    pub async fn export(&self, path: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `export` with a borrowed, reusable `ContainerExportOpts` value."]
    pub async fn export_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerExportOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("export");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.platform_variants {
            query.arg_id_input("platformVariants", value.clone())
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Exports the container as an image to the host's container image store.\n\nSelects GraphQL Wire_Name `exportImage` on `Container`."]
    pub async fn export_image(&self, name: impl Into<String>) -> Result<(), crate::QueryError> {
        let query = self.selection.select("exportImage");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `exportImage` with a borrowed, reusable `ContainerExportImageOpts` value."]
    pub async fn export_image_opts(
        &self,
        name: impl Into<String>,
        opts: &ContainerExportImageOpts,
    ) -> Result<(), crate::QueryError> {
        let query = self.selection.select("exportImage");
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = if let Some(value) = &opts.platform_variants {
            query.arg_id_input("platformVariants", value.clone())
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the list of exposed ports.\n\nThis includes ports already exposed by the image, even if not explicitly added with dagger.\n\nSelects GraphQL Wire_Name `exposedPorts` on `Container`."]
    pub async fn exposed_ports(&self) -> Result<Vec<super::Port>, crate::QueryError> {
        let query = self.selection.select("exposedPorts");
        let query = query.select("id");
        query
            .execute_reentry::<super::Port, Vec<crate::Id>>(&self.session, "Port")
            .await
    }
    #[doc = "Retrieves a file at the given path.\n\nMounts are included.\n\nSelects GraphQL Wire_Name `file` on `Container`."]
    #[must_use]
    pub fn file(&self, path: impl Into<String>) -> super::File {
        let query = self.selection.select("file");
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `file` with a borrowed, reusable `ContainerFileOpts` value."]
    #[must_use]
    pub fn file_opts(&self, path: impl Into<String>, opts: &ContainerFileOpts) -> super::File {
        let query = self.selection.select("file");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Download a container image, and apply it to the container state. All previous state will be lost.\n\nSelects GraphQL Wire_Name `from` on `Container`."]
    #[must_use]
    pub fn from(&self, address: impl Into<String>) -> super::Container {
        let query = self.selection.select("from");
        let query = query.arg("address", address.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `from` with a borrowed, reusable `ContainerFromOpts` value."]
    #[must_use]
    pub fn from_opts(
        &self,
        address: impl Into<String>,
        opts: &ContainerFromOpts,
    ) -> super::Container {
        let query = self.selection.select("from");
        let query = query.arg("address", address.into());
        let query = if let Some(value) = &opts.insecure_skip_tls_verify {
            query.arg("insecureSkipTLSVerify", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.protocol {
            query.arg("protocol", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.registry_service {
            query.arg_id_input("registryService", value.clone())
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "A unique identifier for this Container.\n\nSelects GraphQL Wire_Name `id` on `Container`."]
    pub async fn id(&self) -> Result<crate::Id, crate::QueryError> {
        let query = self.selection.select("id");
        query.execute(&self.session).await
    }
    #[doc = "The unique image reference which can only be retrieved immediately after the 'Container.From' call.\n\nSelects GraphQL Wire_Name `imageRef` on `Container`."]
    pub async fn image_ref(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("imageRef");
        query.execute(&self.session).await
    }
    #[doc = "Reads the container from an OCI tarball.\n\nSelects GraphQL Wire_Name `import` on `Container`."]
    #[must_use]
    pub fn import(&self, source: impl Into<crate::IdInput<super::File>>) -> super::Container {
        let query = self.selection.select("import");
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `import` with a borrowed, reusable `ContainerImportOpts` value."]
    #[must_use]
    pub fn import_opts(
        &self,
        source: impl Into<crate::IdInput<super::File>>,
        opts: &ContainerImportOpts,
    ) -> super::Container {
        let query = self.selection.select("import");
        let query = query.arg_id_input("source", source.into());
        let query = if let Some(value) = &opts.tag {
            query.arg("tag", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the value of the specified label.\n\nSelects GraphQL Wire_Name `label` on `Container`."]
    pub async fn label(
        &self,
        name: impl Into<String>,
    ) -> Result<Option<String>, crate::QueryError> {
        let query = self.selection.select("label");
        let query = query.arg("name", name.into());
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the list of labels passed to container.\n\nSelects GraphQL Wire_Name `labels` on `Container`."]
    pub async fn labels(&self) -> Result<Vec<super::Label>, crate::QueryError> {
        let query = self.selection.select("labels");
        let query = query.select("id");
        query
            .execute_reentry::<super::Label, Vec<crate::Id>>(&self.session, "Label")
            .await
    }
    #[doc = "Returns the image layer or configuration blob with the given digest as a File.\n\nSelects GraphQL Wire_Name `layer` on `Container`."]
    #[must_use]
    pub fn layer(&self, id: impl Into<String>) -> super::File {
        let query = self.selection.select("layer");
        let query = query.arg("id", id.into());
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `layer` with a borrowed, reusable `ContainerLayerOpts` value."]
    #[must_use]
    pub fn layer_opts(&self, id: impl Into<String>, opts: &ContainerLayerOpts) -> super::File {
        let query = self.selection.select("layer");
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = query.arg("id", id.into());
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Computes and returns the manifest for this container as a File.\n\nSelects GraphQL Wire_Name `manifest` on `Container`."]
    #[must_use]
    pub fn manifest(&self) -> super::File {
        let query = self.selection.select("manifest");
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `manifest` with a borrowed, reusable `ContainerManifestOpts` value."]
    #[must_use]
    pub fn manifest_opts(&self, opts: &ContainerManifestOpts) -> super::File {
        let query = self.selection.select("manifest");
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        super::File {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the list of paths where a directory is mounted.\n\nSelects GraphQL Wire_Name `mounts` on `Container`."]
    pub async fn mounts(&self) -> Result<Vec<String>, crate::QueryError> {
        let query = self.selection.select("mounts");
        query.execute(&self.session).await
    }
    #[doc = "The platform this container executes and publishes as.\n\nSelects GraphQL Wire_Name `platform` on `Container`."]
    pub async fn platform(&self) -> Result<crate::Platform, crate::QueryError> {
        let query = self.selection.select("platform");
        query.execute(&self.session).await
    }
    #[doc = "Package the container state as an OCI image, and publish it to a registry\n\nReturns the fully qualified address of the published image, with digest\n\nSelects GraphQL Wire_Name `publish` on `Container`."]
    pub async fn publish(&self, address: impl Into<String>) -> Result<String, crate::QueryError> {
        let query = self.selection.select("publish");
        let query = query.arg("address", address.into());
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `publish` with a borrowed, reusable `ContainerPublishOpts` value."]
    pub async fn publish_opts(
        &self,
        address: impl Into<String>,
        opts: &ContainerPublishOpts,
    ) -> Result<String, crate::QueryError> {
        let query = self.selection.select("publish");
        let query = query.arg("address", address.into());
        let query = if let Some(value) = &opts.forced_compression {
            query.arg("forcedCompression", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_skip_tls_verify {
            query.arg("insecureSkipTLSVerify", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.media_types {
            query.arg("mediaTypes", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.platform_variants {
            query.arg_id_input("platformVariants", value.clone())
        } else {
            query
        };
        let query = if let Some(value) = &opts.protocol {
            query.arg("protocol", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.registry_service {
            query.arg_id_input("registryService", value.clone())
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Return a snapshot of the container's root filesystem. The snapshot can be modified then written back using withRootfs. Use that method for filesystem modifications.\n\nSelects GraphQL Wire_Name `rootfs` on `Container`."]
    #[must_use]
    pub fn rootfs(&self) -> super::Directory {
        let query = self.selection.select("rootfs");
        super::Directory {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return file status\n\nSelects GraphQL Wire_Name `stat` on `Container`."]
    pub async fn stat(
        &self,
        path: impl Into<String>,
    ) -> Result<Option<super::Stat>, crate::QueryError> {
        let query = self.selection.select("stat");
        let query = query.arg("path", path.into());
        let query = query.select("id");
        query
            .execute_reentry::<super::Stat, Option<crate::Id>>(&self.session, "Stat")
            .await
    }
    #[doc = "Executes GraphQL operation `stat` with a borrowed, reusable `ContainerStatOpts` value."]
    pub async fn stat_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerStatOpts,
    ) -> Result<Option<super::Stat>, crate::QueryError> {
        let query = self.selection.select("stat");
        let query = if let Some(value) = &opts.do_not_follow_symlinks {
            query.arg("doNotFollowSymlinks", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.select("id");
        query
            .execute_reentry::<super::Stat, Option<crate::Id>>(&self.session, "Stat")
            .await
    }
    #[doc = "The buffered standard error stream of the last executed command\n\nReturns an error if no command was executed\n\nSelects GraphQL Wire_Name `stderr` on `Container`."]
    pub async fn stderr(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("stderr");
        query.execute(&self.session).await
    }
    #[doc = "The buffered standard output stream of the last executed command\n\nReturns an error if no command was executed\n\nSelects GraphQL Wire_Name `stdout` on `Container`."]
    pub async fn stdout(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("stdout");
        query.execute(&self.session).await
    }
    #[doc = "Forces evaluation of the pipeline in the engine.\n\nIt doesn't run the default command if no exec has been set.\n\nSelects GraphQL Wire_Name `sync` on `Container`."]
    pub async fn sync(&self) -> Result<super::Container, crate::QueryError> {
        let query = self.selection.select("sync");
        let id: crate::Id = query.execute(&self.session).await?;
        Ok(crate::query::reenter::<super::Container>(
            &self.session,
            id,
            "Container",
        ))
    }
    #[doc = "Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).\n\nSelects GraphQL Wire_Name `terminal` on `Container`."]
    #[must_use]
    pub fn terminal(&self) -> super::Container {
        let query = self.selection.select("terminal");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `terminal` with a borrowed, reusable `ContainerTerminalOpts` value."]
    #[must_use]
    pub fn terminal_opts(&self, opts: &ContainerTerminalOpts) -> super::Container {
        let query = self.selection.select("terminal");
        let query = if let Some(value) = &opts.cmd {
            query.arg("cmd", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.\n\nBe sure to set any exposed ports before calling this api.\n\nSelects GraphQL Wire_Name `up` on `Container`."]
    pub async fn up(&self) -> Result<(), crate::QueryError> {
        let query = self.selection.select("up");
        query.execute(&self.session).await
    }
    #[doc = "Executes GraphQL operation `up` with a borrowed, reusable `ContainerUpOpts` value."]
    pub async fn up_opts(&self, opts: &ContainerUpOpts) -> Result<(), crate::QueryError> {
        let query = self.selection.select("up");
        let query = if let Some(value) = &opts.args {
            query.arg("args", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_init {
            query.arg("noInit", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.ports {
            query.arg("ports", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.random {
            query.arg("random", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.use_entrypoint {
            query.arg("useEntrypoint", value)
        } else {
            query
        };
        query.execute(&self.session).await
    }
    #[doc = "Retrieves the user to be set for all commands.\n\nSelects GraphQL Wire_Name `user` on `Container`."]
    pub async fn user(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("user");
        query.execute(&self.session).await
    }
    #[doc = "Retrieves this container plus the given OCI annotation.\n\nSelects GraphQL Wire_Name `withAnnotation` on `Container`."]
    #[must_use]
    pub fn with_annotation(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withAnnotation");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Configures default arguments for future commands. Like CMD in Dockerfile.\n\nSelects GraphQL Wire_Name `withDefaultArgs` on `Container`."]
    #[must_use]
    pub fn with_default_args(&self, args: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withDefaultArgs");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set the default command to invoke for the container's terminal API.\n\nSelects GraphQL Wire_Name `withDefaultTerminalCmd` on `Container`."]
    #[must_use]
    pub fn with_default_terminal_cmd(&self, args: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withDefaultTerminalCmd");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withDefaultTerminalCmd` with a borrowed, reusable `ContainerWithDefaultTerminalCmdOpts` value."]
    #[must_use]
    pub fn with_default_terminal_cmd_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: &ContainerWithDefaultTerminalCmdOpts,
    ) -> super::Container {
        let query = self.selection.select("withDefaultTerminalCmd");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a new container snapshot, with a directory added to its filesystem\n\nSelects GraphQL Wire_Name `withDirectory` on `Container`."]
    #[must_use]
    pub fn with_directory(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::Container {
        let query = self.selection.select("withDirectory");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withDirectory` with a borrowed, reusable `ContainerWithDirectoryOpts` value."]
    #[must_use]
    pub fn with_directory_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
        opts: &ContainerWithDirectoryOpts,
    ) -> super::Container {
        let query = self.selection.select("withDirectory");
        let query = if let Some(value) = &opts.exclude {
            query.arg("exclude", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.gitignore {
            query.arg("gitignore", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.include {
            query.arg("include", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container with the specificed docker healtcheck command set.\n\nSelects GraphQL Wire_Name `withDockerHealthcheck` on `Container`."]
    #[must_use]
    pub fn with_docker_healthcheck(&self, args: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withDockerHealthcheck");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withDockerHealthcheck` with a borrowed, reusable `ContainerWithDockerHealthcheckOpts` value."]
    #[must_use]
    pub fn with_docker_healthcheck_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: &ContainerWithDockerHealthcheckOpts,
    ) -> super::Container {
        let query = self.selection.select("withDockerHealthcheck");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        let query = if let Some(value) = &opts.interval {
            query.arg("interval", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.retries {
            query.arg("retries", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.shell {
            query.arg("shell", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.start_interval {
            query.arg("startInterval", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.start_period {
            query.arg("startPeriod", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.timeout {
            query.arg("timeout", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set an OCI-style entrypoint. It will be included in the container's OCI configuration. Note, withExec ignores the entrypoint by default.\n\nSelects GraphQL Wire_Name `withEntrypoint` on `Container`."]
    #[must_use]
    pub fn with_entrypoint(&self, args: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withEntrypoint");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withEntrypoint` with a borrowed, reusable `ContainerWithEntrypointOpts` value."]
    #[must_use]
    pub fn with_entrypoint_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: &ContainerWithEntrypointOpts,
    ) -> super::Container {
        let query = self.selection.select("withEntrypoint");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        let query = if let Some(value) = &opts.keep_default_args {
            query.arg("keepDefaultArgs", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Export environment variables from an env-file to the container.\n\nSelects GraphQL Wire_Name `withEnvFileVariables` on `Container`."]
    #[must_use]
    pub fn with_env_file_variables(
        &self,
        source: impl Into<crate::IdInput<super::EnvFile>>,
    ) -> super::Container {
        let query = self.selection.select("withEnvFileVariables");
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set a new environment variable in the container.\n\nSelects GraphQL Wire_Name `withEnvVariable` on `Container`."]
    #[must_use]
    pub fn with_env_variable(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withEnvVariable");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withEnvVariable` with a borrowed, reusable `ContainerWithEnvVariableOpts` value."]
    #[must_use]
    pub fn with_env_variable_opts(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
        opts: &ContainerWithEnvVariableOpts,
    ) -> super::Container {
        let query = self.selection.select("withEnvVariable");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Raise an error.\n\nSelects GraphQL Wire_Name `withError` on `Container`."]
    #[must_use]
    pub fn with_error(&self, err: impl Into<String>) -> super::Container {
        let query = self.selection.select("withError");
        let query = query.arg("err", err.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Execute a command in the container, and return a new snapshot of the container state after execution.\n\nSelects GraphQL Wire_Name `withExec` on `Container`."]
    #[must_use]
    pub fn with_exec(&self, args: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withExec");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withExec` with a borrowed, reusable `ContainerWithExecOpts` value."]
    #[must_use]
    pub fn with_exec_opts(
        &self,
        args: Vec<impl Into<String>>,
        opts: &ContainerWithExecOpts,
    ) -> super::Container {
        let query = self.selection.select("withExec");
        let args = args.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("args", args);
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.expect {
            query.arg("expect", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_privileged_nesting {
            query.arg("experimentalPrivilegedNesting", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.insecure_root_capabilities {
            query.arg("insecureRootCapabilities", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.no_init {
            query.arg("noInit", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.redirect_stderr {
            query.arg("redirectStderr", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.redirect_stdin {
            query.arg("redirectStdin", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.redirect_stdout {
            query.arg("redirectStdout", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.stdin {
            query.arg("stdin", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.use_entrypoint {
            query.arg("useEntrypoint", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Expose a network port. Like EXPOSE in Dockerfile (but with healthcheck support)\n\nExposed ports serve two purposes:\n\n- For health checks and introspection, when running services\n\n- For setting the EXPOSE OCI field when publishing the container\n\nSelects GraphQL Wire_Name `withExposedPort` on `Container`."]
    #[must_use]
    pub fn with_exposed_port(&self, port: i64) -> super::Container {
        let query = self.selection.select("withExposedPort");
        let query = query.arg("port", port);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withExposedPort` with a borrowed, reusable `ContainerWithExposedPortOpts` value."]
    #[must_use]
    pub fn with_exposed_port_opts(
        &self,
        port: i64,
        opts: &ContainerWithExposedPortOpts,
    ) -> super::Container {
        let query = self.selection.select("withExposedPort");
        let query = if let Some(value) = &opts.description {
            query.arg("description", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.experimental_skip_healthcheck {
            query.arg("experimentalSkipHealthcheck", value)
        } else {
            query
        };
        let query = query.arg("port", port);
        let query = if let Some(value) = &opts.protocol {
            query.arg("protocol", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a container snapshot with a file added\n\nSelects GraphQL Wire_Name `withFile` on `Container`."]
    #[must_use]
    pub fn with_file(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
    ) -> super::Container {
        let query = self.selection.select("withFile");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withFile` with a borrowed, reusable `ContainerWithFileOpts` value."]
    #[must_use]
    pub fn with_file_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
        opts: &ContainerWithFileOpts,
    ) -> super::Container {
        let query = self.selection.select("withFile");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus the contents of the given files copied to the given path.\n\nSelects GraphQL Wire_Name `withFiles` on `Container`."]
    #[must_use]
    pub fn with_files(
        &self,
        path: impl Into<String>,
        sources: Vec<crate::IdInput<super::File>>,
    ) -> super::Container {
        let query = self.selection.select("withFiles");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("sources", sources);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withFiles` with a borrowed, reusable `ContainerWithFilesOpts` value."]
    #[must_use]
    pub fn with_files_opts(
        &self,
        path: impl Into<String>,
        sources: Vec<crate::IdInput<super::File>>,
        opts: &ContainerWithFilesOpts,
    ) -> super::Container {
        let query = self.selection.select("withFiles");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        let query = query.arg_id_input("sources", sources);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus the given label.\n\nSelects GraphQL Wire_Name `withLabel` on `Container`."]
    #[must_use]
    pub fn with_label(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withLabel");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a cache volume mounted at the given path.\n\nSelects GraphQL Wire_Name `withMountedCache` on `Container`."]
    #[must_use]
    pub fn with_mounted_cache(
        &self,
        cache: impl Into<crate::IdInput<super::CacheVolume>>,
        path: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withMountedCache");
        let query = query.arg_id_input("cache", cache.into());
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedCache` with a borrowed, reusable `ContainerWithMountedCacheOpts` value."]
    #[must_use]
    pub fn with_mounted_cache_opts(
        &self,
        cache: impl Into<crate::IdInput<super::CacheVolume>>,
        path: impl Into<String>,
        opts: &ContainerWithMountedCacheOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedCache");
        let query = query.arg_id_input("cache", cache.into());
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.sharing {
            query.arg("sharing", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.source {
            query.arg_id_input("source", value.clone())
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a directory mounted at the given path.\n\nSelects GraphQL Wire_Name `withMountedDirectory` on `Container`."]
    #[must_use]
    pub fn with_mounted_directory(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::Container {
        let query = self.selection.select("withMountedDirectory");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedDirectory` with a borrowed, reusable `ContainerWithMountedDirectoryOpts` value."]
    #[must_use]
    pub fn with_mounted_directory_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Directory>>,
        opts: &ContainerWithMountedDirectoryOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedDirectory");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.read_only {
            query.arg("readOnly", value)
        } else {
            query
        };
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a file mounted at the given path.\n\nSelects GraphQL Wire_Name `withMountedFile` on `Container`."]
    #[must_use]
    pub fn with_mounted_file(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
    ) -> super::Container {
        let query = self.selection.select("withMountedFile");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedFile` with a borrowed, reusable `ContainerWithMountedFileOpts` value."]
    #[must_use]
    pub fn with_mounted_file_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::File>>,
        opts: &ContainerWithMountedFileOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedFile");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a secret mounted into a file at the given path.\n\nSelects GraphQL Wire_Name `withMountedSecret` on `Container`."]
    #[must_use]
    pub fn with_mounted_secret(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Secret>>,
    ) -> super::Container {
        let query = self.selection.select("withMountedSecret");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedSecret` with a borrowed, reusable `ContainerWithMountedSecretOpts` value."]
    #[must_use]
    pub fn with_mounted_secret_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Secret>>,
        opts: &ContainerWithMountedSecretOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedSecret");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.mode {
            query.arg("mode", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.\n\nSelects GraphQL Wire_Name `withMountedTemp` on `Container`."]
    #[must_use]
    pub fn with_mounted_temp(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withMountedTemp");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedTemp` with a borrowed, reusable `ContainerWithMountedTempOpts` value."]
    #[must_use]
    pub fn with_mounted_temp_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithMountedTempOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedTemp");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.size {
            query.arg("size", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a volume mounted at the given path.\n\nSelects GraphQL Wire_Name `withMountedVolume` on `Container`."]
    #[must_use]
    pub fn with_mounted_volume(
        &self,
        path: impl Into<String>,
        volume: impl Into<crate::IdInput<super::Volume>>,
    ) -> super::Container {
        let query = self.selection.select("withMountedVolume");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("volume", volume.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withMountedVolume` with a borrowed, reusable `ContainerWithMountedVolumeOpts` value."]
    #[must_use]
    pub fn with_mounted_volume_opts(
        &self,
        path: impl Into<String>,
        volume: impl Into<crate::IdInput<super::Volume>>,
        opts: &ContainerWithMountedVolumeOpts,
    ) -> super::Container {
        let query = self.selection.select("withMountedVolume");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.read_only {
            query.arg("readOnly", value)
        } else {
            query
        };
        let query = query.arg_id_input("volume", volume.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a new container snapshot, with a file added to its filesystem with text content\n\nSelects GraphQL Wire_Name `withNewFile` on `Container`."]
    #[must_use]
    pub fn with_new_file(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withNewFile` with a borrowed, reusable `ContainerWithNewFileOpts` value."]
    #[must_use]
    pub fn with_new_file_opts(
        &self,
        contents: impl Into<String>,
        path: impl Into<String>,
        opts: &ContainerWithNewFileOpts,
    ) -> super::Container {
        let query = self.selection.select("withNewFile");
        let query = query.arg("contents", contents.into());
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = if let Some(value) = &opts.permissions {
            query.arg("permissions", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Attach credentials for future publishing to a registry. Use in combination with publish\n\nSelects GraphQL Wire_Name `withRegistryAuth` on `Container`."]
    #[must_use]
    pub fn with_registry_auth(
        &self,
        address: impl Into<String>,
        secret: impl Into<crate::IdInput<super::Secret>>,
        username: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withRegistryAuth");
        let query = query.arg("address", address.into());
        let query = query.arg_id_input("secret", secret.into());
        let query = query.arg("username", username.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Change the container's root filesystem. The previous root filesystem will be lost.\n\nSelects GraphQL Wire_Name `withRootfs` on `Container`."]
    #[must_use]
    pub fn with_rootfs(
        &self,
        directory: impl Into<crate::IdInput<super::Directory>>,
    ) -> super::Container {
        let query = self.selection.select("withRootfs");
        let query = query.arg_id_input("directory", directory.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set a new environment variable, using a secret value\n\nSelects GraphQL Wire_Name `withSecretVariable` on `Container`."]
    #[must_use]
    pub fn with_secret_variable(
        &self,
        name: impl Into<String>,
        secret: impl Into<crate::IdInput<super::Secret>>,
    ) -> super::Container {
        let query = self.selection.select("withSecretVariable");
        let query = query.arg("name", name.into());
        let query = query.arg_id_input("secret", secret.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Establish a runtime dependency from a container to a network service.\n\nThe service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.\n\nThe service will be reachable from the container via the provided hostname alias.\n\nThe service dependency will also convey to any files or directories produced by the container.\n\nSelects GraphQL Wire_Name `withServiceBinding` on `Container`."]
    #[must_use]
    pub fn with_service_binding(
        &self,
        alias: impl Into<String>,
        service: impl Into<crate::IdInput<super::Service>>,
    ) -> super::Container {
        let query = self.selection.select("withServiceBinding");
        let query = query.arg("alias", alias.into());
        let query = query.arg_id_input("service", service.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a snapshot with a symlink\n\nSelects GraphQL Wire_Name `withSymlink` on `Container`."]
    #[must_use]
    pub fn with_symlink(
        &self,
        link_name: impl Into<String>,
        target: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withSymlink");
        let query = query.arg("linkName", link_name.into());
        let query = query.arg("target", target.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withSymlink` with a borrowed, reusable `ContainerWithSymlinkOpts` value."]
    #[must_use]
    pub fn with_symlink_opts(
        &self,
        link_name: impl Into<String>,
        target: impl Into<String>,
        opts: &ContainerWithSymlinkOpts,
    ) -> super::Container {
        let query = self.selection.select("withSymlink");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("linkName", link_name.into());
        let query = query.arg("target", target.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container plus a socket forwarded to the given Unix socket path.\n\nSelects GraphQL Wire_Name `withUnixSocket` on `Container`."]
    #[must_use]
    pub fn with_unix_socket(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Socket>>,
    ) -> super::Container {
        let query = self.selection.select("withUnixSocket");
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withUnixSocket` with a borrowed, reusable `ContainerWithUnixSocketOpts` value."]
    #[must_use]
    pub fn with_unix_socket_opts(
        &self,
        path: impl Into<String>,
        source: impl Into<crate::IdInput<super::Socket>>,
        opts: &ContainerWithUnixSocketOpts,
    ) -> super::Container {
        let query = self.selection.select("withUnixSocket");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.inherit_owner {
            query.arg("inheritOwner", value)
        } else {
            query
        };
        let query = if let Some(value) = &opts.owner {
            query.arg("owner", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        let query = query.arg_id_input("source", source.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container with a different command user.\n\nSelects GraphQL Wire_Name `withUser` on `Container`."]
    #[must_use]
    pub fn with_user(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withUser");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Set a new non-secret environment variable for future execs without invalidating exec cache when only its value changes.\n\nThis is an expert-only escape hatch. If a volatile value affects observable exec results, stale cached results may be reused.\n\nSelects GraphQL Wire_Name `withVolatileVariable` on `Container`."]
    #[must_use]
    pub fn with_volatile_variable(
        &self,
        name: impl Into<String>,
        value: impl Into<String>,
    ) -> super::Container {
        let query = self.selection.select("withVolatileVariable");
        let query = query.arg("name", name.into());
        let query = query.arg("value", value.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Change the container's working directory. Like WORKDIR in Dockerfile.\n\nSelects GraphQL Wire_Name `withWorkdir` on `Container`."]
    #[must_use]
    pub fn with_workdir(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withWorkdir");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withWorkdir` with a borrowed, reusable `ContainerWithWorkdirOpts` value."]
    #[must_use]
    pub fn with_workdir_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithWorkdirOpts,
    ) -> super::Container {
        let query = self.selection.select("withWorkdir");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container minus the given OCI annotation.\n\nSelects GraphQL Wire_Name `withoutAnnotation` on `Container`."]
    #[must_use]
    pub fn without_annotation(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutAnnotation");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Remove the container's default arguments.\n\nSelects GraphQL Wire_Name `withoutDefaultArgs` on `Container`."]
    #[must_use]
    pub fn without_default_args(&self) -> super::Container {
        let query = self.selection.select("withoutDefaultArgs");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a new container snapshot, with a directory removed from its filesystem\n\nSelects GraphQL Wire_Name `withoutDirectory` on `Container`."]
    #[must_use]
    pub fn without_directory(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutDirectory");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutDirectory` with a borrowed, reusable `ContainerWithoutDirectoryOpts` value."]
    #[must_use]
    pub fn without_directory_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithoutDirectoryOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutDirectory");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container without a configured docker healtcheck command.\n\nSelects GraphQL Wire_Name `withoutDockerHealthcheck` on `Container`."]
    #[must_use]
    pub fn without_docker_healthcheck(&self) -> super::Container {
        let query = self.selection.select("withoutDockerHealthcheck");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Reset the container's OCI entrypoint.\n\nSelects GraphQL Wire_Name `withoutEntrypoint` on `Container`."]
    #[must_use]
    pub fn without_entrypoint(&self) -> super::Container {
        let query = self.selection.select("withoutEntrypoint");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutEntrypoint` with a borrowed, reusable `ContainerWithoutEntrypointOpts` value."]
    #[must_use]
    pub fn without_entrypoint_opts(
        &self,
        opts: &ContainerWithoutEntrypointOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutEntrypoint");
        let query = if let Some(value) = &opts.keep_default_args {
            query.arg("keepDefaultArgs", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container minus the given environment variable.\n\nSelects GraphQL Wire_Name `withoutEnvVariable` on `Container`."]
    #[must_use]
    pub fn without_env_variable(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutEnvVariable");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Unexpose a previously exposed port.\n\nSelects GraphQL Wire_Name `withoutExposedPort` on `Container`."]
    #[must_use]
    pub fn without_exposed_port(&self, port: i64) -> super::Container {
        let query = self.selection.select("withoutExposedPort");
        let query = query.arg("port", port);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutExposedPort` with a borrowed, reusable `ContainerWithoutExposedPortOpts` value."]
    #[must_use]
    pub fn without_exposed_port_opts(
        &self,
        port: i64,
        opts: &ContainerWithoutExposedPortOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutExposedPort");
        let query = query.arg("port", port);
        let query = if let Some(value) = &opts.protocol {
            query.arg("protocol", value)
        } else {
            query
        };
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container with the file at the given path removed.\n\nSelects GraphQL Wire_Name `withoutFile` on `Container`."]
    #[must_use]
    pub fn without_file(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutFile");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutFile` with a borrowed, reusable `ContainerWithoutFileOpts` value."]
    #[must_use]
    pub fn without_file_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithoutFileOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutFile");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Return a new container spanshot with specified files removed\n\nSelects GraphQL Wire_Name `withoutFiles` on `Container`."]
    #[must_use]
    pub fn without_files(&self, paths: Vec<impl Into<String>>) -> super::Container {
        let query = self.selection.select("withoutFiles");
        let paths = paths.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("paths", paths);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutFiles` with a borrowed, reusable `ContainerWithoutFilesOpts` value."]
    #[must_use]
    pub fn without_files_opts(
        &self,
        paths: Vec<impl Into<String>>,
        opts: &ContainerWithoutFilesOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutFiles");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let paths = paths.into_iter().map(Into::into).collect::<Vec<String>>();
        let query = query.arg("paths", paths);
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container minus the given environment label.\n\nSelects GraphQL Wire_Name `withoutLabel` on `Container`."]
    #[must_use]
    pub fn without_label(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutLabel");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container after unmounting everything at the given path.\n\nSelects GraphQL Wire_Name `withoutMount` on `Container`."]
    #[must_use]
    pub fn without_mount(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutMount");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutMount` with a borrowed, reusable `ContainerWithoutMountOpts` value."]
    #[must_use]
    pub fn without_mount_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithoutMountOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutMount");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container without the registry authentication of a given address.\n\nSelects GraphQL Wire_Name `withoutRegistryAuth` on `Container`."]
    #[must_use]
    pub fn without_registry_auth(&self, address: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutRegistryAuth");
        let query = query.arg("address", address.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container minus the given environment variable containing the secret.\n\nSelects GraphQL Wire_Name `withoutSecretVariable` on `Container`."]
    #[must_use]
    pub fn without_secret_variable(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutSecretVariable");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container with a previously added Unix socket removed.\n\nSelects GraphQL Wire_Name `withoutUnixSocket` on `Container`."]
    #[must_use]
    pub fn without_unix_socket(&self, path: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutUnixSocket");
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Executes GraphQL operation `withoutUnixSocket` with a borrowed, reusable `ContainerWithoutUnixSocketOpts` value."]
    #[must_use]
    pub fn without_unix_socket_opts(
        &self,
        path: impl Into<String>,
        opts: &ContainerWithoutUnixSocketOpts,
    ) -> super::Container {
        let query = self.selection.select("withoutUnixSocket");
        let query = if let Some(value) = &opts.expand {
            query.arg("expand", value)
        } else {
            query
        };
        let query = query.arg("path", path.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container with an unset command user.\n\nShould default to root.\n\nSelects GraphQL Wire_Name `withoutUser` on `Container`."]
    #[must_use]
    pub fn without_user(&self) -> super::Container {
        let query = self.selection.select("withoutUser");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves this container minus the given volatile environment variable.\n\nSelects GraphQL Wire_Name `withoutVolatileVariable` on `Container`."]
    #[must_use]
    pub fn without_volatile_variable(&self, name: impl Into<String>) -> super::Container {
        let query = self.selection.select("withoutVolatileVariable");
        let query = query.arg("name", name.into());
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Unset the container's working directory.\n\nShould default to \"/\".\n\nSelects GraphQL Wire_Name `withoutWorkdir` on `Container`."]
    #[must_use]
    pub fn without_workdir(&self) -> super::Container {
        let query = self.selection.select("withoutWorkdir");
        super::Container {
            session: self.session.clone(),
            selection: query,
        }
    }
    #[doc = "Retrieves the working directory for all commands.\n\nSelects GraphQL Wire_Name `workdir` on `Container`."]
    pub async fn workdir(&self) -> Result<String, crate::QueryError> {
        let query = self.selection.select("workdir");
        query.execute(&self.session).await
    }
}
impl super::Exportable for Container {
    fn export(
        &self,
        path: impl Into<String> + Send,
    ) -> impl core::future::Future<Output = Result<String, crate::QueryError>> + Send {
        let query = self.selection.select("export");
        let query = query.arg("path", path.into());
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Node for Container {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
}
impl super::Syncer for Container {
    fn id(
        &self,
    ) -> impl core::future::Future<Output = Result<crate::Id, crate::QueryError>> + Send {
        let query = self.selection.select("id");
        let session = self.session.clone();
        async move { query.execute(&session).await }
    }
    fn sync(
        &self,
    ) -> impl core::future::Future<Output = Result<super::SyncerClient, crate::QueryError>> + Send
    {
        let query = self.selection.select("sync");
        let session = self.session.clone();
        async move {
            let id: crate::Id = query.execute(&session).await?;
            Ok(crate::query::reenter::<super::SyncerClient>(
                &session, id, "Syncer",
            ))
        }
    }
}
