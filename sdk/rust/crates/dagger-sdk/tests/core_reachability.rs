//! Compile-only reachability proof for every generated public Rust symbol.
// @generated {"format":"dagger-rust-client-v1","ownership":"dagger-codegen","schema_digest":"sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306","target_revision":"25300124ca110612edc09c43f89cb5fad6028170"}
#![cfg(feature = "gen")]
use dagger_sdk::*;
fn generated_value<T>() -> T {
    panic!("compile-only generated value")
}
#[allow(deprecated)]
fn reach_address(value: &Address) {
    drop(value.container());
    drop(value.directory());
    let opts = AddressDirectoryOpts::default();
    drop(value.directory_opts(&opts));
    let _ = &opts.exclude;
    drop(AddressDirectoryOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(AddressDirectoryOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(AddressDirectoryOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_cache;
    drop(AddressDirectoryOpts::default().with_no_cache(generated_value::<bool>()));
    drop(value.file());
    let opts = AddressFileOpts::default();
    drop(value.file_opts(&opts));
    let _ = &opts.exclude;
    drop(AddressFileOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(AddressFileOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(AddressFileOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_cache;
    drop(AddressFileOpts::default().with_no_cache(generated_value::<bool>()));
    drop(value.git_ref());
    drop(value.git_repository());
    drop(value.id());
    drop(value.secret());
    drop(value.service());
    drop(value.socket());
    drop(value.value());
    drop(value.volume());
}
#[allow(deprecated)]
fn reach_buildarg_input(value: &BuildArg) {
    drop(BuildArg::new(String::new(), String::new()));
    let _ = &value.name;
    let _ = &value.value;
}
#[allow(deprecated)]
fn reach_cachevolume(value: &CacheVolume) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_changeset(value: &Changeset) {
    drop(value.added_paths());
    drop(value.after());
    drop(value.as_patch());
    drop(value.before());
    drop(value.diff_stats());
    drop(value.export(String::new()));
    drop(value.id());
    drop(value.is_empty());
    drop(value.layer());
    drop(value.modified_paths());
    drop(value.removed_paths());
    drop(value.sync());
    drop(value.with_changeset(Id::from("generated-id")));
    let opts = ChangesetWithChangesetOpts::default();
    drop(value.with_changeset_opts(Id::from("generated-id"), &opts));
    let _ = &opts.on_conflict;
    drop(
        ChangesetWithChangesetOpts::default()
            .with_on_conflict(generated_value::<ChangesetMergeConflict>()),
    );
    drop(value.with_changesets(generated_value::<Vec<dagger_sdk::IdInput<Changeset>>>()));
    let opts = ChangesetWithChangesetsOpts::default();
    drop(value.with_changesets_opts(
        generated_value::<Vec<dagger_sdk::IdInput<Changeset>>>(),
        &opts,
    ));
    let _ = &opts.on_conflict;
    drop(
        ChangesetWithChangesetsOpts::default()
            .with_on_conflict(generated_value::<ChangesetsMergeConflict>()),
    );
}
#[allow(deprecated)]
fn reach_check(value: &Check) {
    drop(value.check_type());
    drop(value.completed());
    drop(value.description());
    drop(value.error());
    drop(value.id());
    drop(value.name());
    drop(value.original_module());
    drop(value.passed());
    drop(value.path());
    drop(value.result_emoji());
    drop(value.run());
}
#[allow(deprecated)]
fn reach_checkgroup(value: &CheckGroup) {
    drop(value.id());
    drop(value.list());
    drop(value.report());
    drop(value.run());
    let opts = CheckGroupRunOpts::default();
    drop(value.run_opts(&opts));
    let _ = &opts.fail_fast;
    drop(CheckGroupRunOpts::default().with_fail_fast(generated_value::<bool>()));
}
#[allow(deprecated)]
fn reach_clientfilesyncmirror(value: &ClientFilesyncMirror) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_cloud(value: &Cloud) {
    drop(value.id());
    drop(value.trace_url());
}
#[allow(deprecated)]
fn reach_container(value: &Container) {
    drop(value.as_service());
    let opts = ContainerAsServiceOpts::default();
    drop(value.as_service_opts(&opts));
    let _ = &opts.args;
    drop(ContainerAsServiceOpts::default().with_args(generated_value::<Vec<String>>()));
    let _ = &opts.expand;
    drop(ContainerAsServiceOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        ContainerAsServiceOpts::default()
            .with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(
        ContainerAsServiceOpts::default()
            .with_insecure_root_capabilities(generated_value::<bool>()),
    );
    let _ = &opts.no_init;
    drop(ContainerAsServiceOpts::default().with_no_init(generated_value::<bool>()));
    let _ = &opts.use_entrypoint;
    drop(ContainerAsServiceOpts::default().with_use_entrypoint(generated_value::<bool>()));
    drop(value.as_tarball());
    let opts = ContainerAsTarballOpts::default();
    drop(value.as_tarball_opts(&opts));
    let _ = &opts.forced_compression;
    drop(
        ContainerAsTarballOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.media_types;
    drop(ContainerAsTarballOpts::default().with_media_types(generated_value::<ImageMediaTypes>()));
    let _ = &opts.platform_variants;
    drop(
        ContainerAsTarballOpts::default()
            .with_platform_variants(generated_value::<Vec<dagger_sdk::IdInput<Container>>>()),
    );
    drop(value.combined_output());
    drop(value.default_args());
    drop(value.directory(String::new()));
    let opts = ContainerDirectoryOpts::default();
    drop(value.directory_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerDirectoryOpts::default().with_expand(generated_value::<bool>()));
    drop(value.docker_healthcheck());
    drop(value.entrypoint());
    drop(value.env_variable(String::new()));
    drop(value.env_variables());
    drop(value.exists(String::new()));
    let opts = ContainerExistsOpts::default();
    drop(value.exists_opts(String::new(), &opts));
    let _ = &opts.do_not_follow_symlinks;
    drop(ContainerExistsOpts::default().with_do_not_follow_symlinks(generated_value::<bool>()));
    let _ = &opts.expand;
    drop(ContainerExistsOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.expected_type;
    drop(ContainerExistsOpts::default().with_expected_type(generated_value::<ExistsType>()));
    drop(value.exit_code());
    drop(value.experimental_with_all_gp_us());
    drop(value.experimental_with_gpu(generated_value::<Vec<String>>()));
    drop(value.export(String::new()));
    let opts = ContainerExportOpts::default();
    drop(value.export_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerExportOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.forced_compression;
    drop(
        ContainerExportOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.media_types;
    drop(ContainerExportOpts::default().with_media_types(generated_value::<ImageMediaTypes>()));
    let _ = &opts.platform_variants;
    drop(
        ContainerExportOpts::default()
            .with_platform_variants(generated_value::<Vec<dagger_sdk::IdInput<Container>>>()),
    );
    drop(value.export_image(String::new()));
    let opts = ContainerExportImageOpts::default();
    drop(value.export_image_opts(String::new(), &opts));
    let _ = &opts.forced_compression;
    drop(
        ContainerExportImageOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.media_types;
    drop(
        ContainerExportImageOpts::default().with_media_types(generated_value::<ImageMediaTypes>()),
    );
    let _ = &opts.platform_variants;
    drop(
        ContainerExportImageOpts::default()
            .with_platform_variants(generated_value::<Vec<dagger_sdk::IdInput<Container>>>()),
    );
    drop(value.exposed_ports());
    drop(value.file(String::new()));
    let opts = ContainerFileOpts::default();
    drop(value.file_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerFileOpts::default().with_expand(generated_value::<bool>()));
    drop(value.from(String::new()));
    let opts = ContainerFromOpts::default();
    drop(value.from_opts(String::new(), &opts));
    let _ = &opts.insecure_skip_tls_verify;
    drop(ContainerFromOpts::default().with_insecure_skip_tls_verify(generated_value::<bool>()));
    let _ = &opts.protocol;
    drop(ContainerFromOpts::default().with_protocol(generated_value::<RegistryProtocol>()));
    let _ = &opts.registry_service;
    drop(ContainerFromOpts::default().with_registry_service(Id::from("generated-id").into()));
    drop(value.id());
    drop(value.image_ref());
    drop(value.import(Id::from("generated-id")));
    let opts = ContainerImportOpts::default();
    drop(value.import_opts(Id::from("generated-id"), &opts));
    let _ = &opts.tag;
    drop(ContainerImportOpts::default().with_tag(String::new()));
    drop(value.label(String::new()));
    drop(value.labels());
    drop(value.layer(String::new()));
    let opts = ContainerLayerOpts::default();
    drop(value.layer_opts(String::new(), &opts));
    let _ = &opts.forced_compression;
    drop(
        ContainerLayerOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.media_types;
    drop(ContainerLayerOpts::default().with_media_types(generated_value::<ImageMediaTypes>()));
    drop(value.manifest());
    let opts = ContainerManifestOpts::default();
    drop(value.manifest_opts(&opts));
    let _ = &opts.forced_compression;
    drop(
        ContainerManifestOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.media_types;
    drop(ContainerManifestOpts::default().with_media_types(generated_value::<ImageMediaTypes>()));
    drop(value.mounts());
    drop(value.platform());
    drop(value.publish(String::new()));
    let opts = ContainerPublishOpts::default();
    drop(value.publish_opts(String::new(), &opts));
    let _ = &opts.forced_compression;
    drop(
        ContainerPublishOpts::default()
            .with_forced_compression(generated_value::<ImageLayerCompression>()),
    );
    let _ = &opts.insecure_skip_tls_verify;
    drop(ContainerPublishOpts::default().with_insecure_skip_tls_verify(generated_value::<bool>()));
    let _ = &opts.media_types;
    drop(ContainerPublishOpts::default().with_media_types(generated_value::<ImageMediaTypes>()));
    let _ = &opts.platform_variants;
    drop(
        ContainerPublishOpts::default()
            .with_platform_variants(generated_value::<Vec<dagger_sdk::IdInput<Container>>>()),
    );
    let _ = &opts.protocol;
    drop(ContainerPublishOpts::default().with_protocol(generated_value::<RegistryProtocol>()));
    let _ = &opts.registry_service;
    drop(ContainerPublishOpts::default().with_registry_service(Id::from("generated-id").into()));
    drop(value.rootfs());
    drop(value.stat(String::new()));
    let opts = ContainerStatOpts::default();
    drop(value.stat_opts(String::new(), &opts));
    let _ = &opts.do_not_follow_symlinks;
    drop(ContainerStatOpts::default().with_do_not_follow_symlinks(generated_value::<bool>()));
    drop(value.stderr());
    drop(value.stdout());
    drop(value.sync());
    drop(value.terminal());
    let opts = ContainerTerminalOpts::default();
    drop(value.terminal_opts(&opts));
    let _ = &opts.cmd;
    drop(ContainerTerminalOpts::default().with_cmd(generated_value::<Vec<String>>()));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        ContainerTerminalOpts::default()
            .with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(
        ContainerTerminalOpts::default().with_insecure_root_capabilities(generated_value::<bool>()),
    );
    drop(value.up());
    let opts = ContainerUpOpts::default();
    drop(value.up_opts(&opts));
    let _ = &opts.args;
    drop(ContainerUpOpts::default().with_args(generated_value::<Vec<String>>()));
    let _ = &opts.expand;
    drop(ContainerUpOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        ContainerUpOpts::default().with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(ContainerUpOpts::default().with_insecure_root_capabilities(generated_value::<bool>()));
    let _ = &opts.no_init;
    drop(ContainerUpOpts::default().with_no_init(generated_value::<bool>()));
    let _ = &opts.ports;
    drop(ContainerUpOpts::default().with_ports(generated_value::<Vec<PortForward>>()));
    let _ = &opts.random;
    drop(ContainerUpOpts::default().with_random(generated_value::<bool>()));
    let _ = &opts.use_entrypoint;
    drop(ContainerUpOpts::default().with_use_entrypoint(generated_value::<bool>()));
    drop(value.user());
    drop(value.with_annotation(String::new(), String::new()));
    drop(value.with_default_args(generated_value::<Vec<String>>()));
    drop(value.with_default_terminal_cmd(generated_value::<Vec<String>>()));
    let opts = ContainerWithDefaultTerminalCmdOpts::default();
    drop(value.with_default_terminal_cmd_opts(generated_value::<Vec<String>>(), &opts));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        ContainerWithDefaultTerminalCmdOpts::default()
            .with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(
        ContainerWithDefaultTerminalCmdOpts::default()
            .with_insecure_root_capabilities(generated_value::<bool>()),
    );
    drop(value.with_directory(String::new(), Id::from("generated-id")));
    let opts = ContainerWithDirectoryOpts::default();
    drop(value.with_directory_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.exclude;
    drop(ContainerWithDirectoryOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.expand;
    drop(ContainerWithDirectoryOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.gitignore;
    drop(ContainerWithDirectoryOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(ContainerWithDirectoryOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithDirectoryOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithDirectoryOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(ContainerWithDirectoryOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_docker_healthcheck(generated_value::<Vec<String>>()));
    let opts = ContainerWithDockerHealthcheckOpts::default();
    drop(value.with_docker_healthcheck_opts(generated_value::<Vec<String>>(), &opts));
    let _ = &opts.interval;
    drop(ContainerWithDockerHealthcheckOpts::default().with_interval(String::new()));
    let _ = &opts.retries;
    drop(ContainerWithDockerHealthcheckOpts::default().with_retries(generated_value::<i64>()));
    let _ = &opts.shell;
    drop(ContainerWithDockerHealthcheckOpts::default().with_shell(generated_value::<bool>()));
    let _ = &opts.start_interval;
    drop(ContainerWithDockerHealthcheckOpts::default().with_start_interval(String::new()));
    let _ = &opts.start_period;
    drop(ContainerWithDockerHealthcheckOpts::default().with_start_period(String::new()));
    let _ = &opts.timeout;
    drop(ContainerWithDockerHealthcheckOpts::default().with_timeout(String::new()));
    drop(value.with_entrypoint(generated_value::<Vec<String>>()));
    let opts = ContainerWithEntrypointOpts::default();
    drop(value.with_entrypoint_opts(generated_value::<Vec<String>>(), &opts));
    let _ = &opts.keep_default_args;
    drop(ContainerWithEntrypointOpts::default().with_keep_default_args(generated_value::<bool>()));
    drop(value.with_env_file_variables(Id::from("generated-id")));
    drop(value.with_env_variable(String::new(), String::new()));
    let opts = ContainerWithEnvVariableOpts::default();
    drop(value.with_env_variable_opts(String::new(), String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithEnvVariableOpts::default().with_expand(generated_value::<bool>()));
    drop(value.with_error(String::new()));
    drop(value.with_exec(generated_value::<Vec<String>>()));
    let opts = ContainerWithExecOpts::default();
    drop(value.with_exec_opts(generated_value::<Vec<String>>(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithExecOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.expect;
    drop(ContainerWithExecOpts::default().with_expect(generated_value::<ReturnType>()));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        ContainerWithExecOpts::default()
            .with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(
        ContainerWithExecOpts::default().with_insecure_root_capabilities(generated_value::<bool>()),
    );
    let _ = &opts.no_init;
    drop(ContainerWithExecOpts::default().with_no_init(generated_value::<bool>()));
    let _ = &opts.redirect_stderr;
    drop(ContainerWithExecOpts::default().with_redirect_stderr(String::new()));
    let _ = &opts.redirect_stdin;
    drop(ContainerWithExecOpts::default().with_redirect_stdin(String::new()));
    let _ = &opts.redirect_stdout;
    drop(ContainerWithExecOpts::default().with_redirect_stdout(String::new()));
    let _ = &opts.stdin;
    drop(ContainerWithExecOpts::default().with_stdin(String::new()));
    let _ = &opts.use_entrypoint;
    drop(ContainerWithExecOpts::default().with_use_entrypoint(generated_value::<bool>()));
    drop(value.with_exposed_port(generated_value::<i64>()));
    let opts = ContainerWithExposedPortOpts::default();
    drop(value.with_exposed_port_opts(generated_value::<i64>(), &opts));
    let _ = &opts.description;
    drop(ContainerWithExposedPortOpts::default().with_description(String::new()));
    let _ = &opts.experimental_skip_healthcheck;
    drop(
        ContainerWithExposedPortOpts::default()
            .with_experimental_skip_healthcheck(generated_value::<bool>()),
    );
    let _ = &opts.protocol;
    drop(
        ContainerWithExposedPortOpts::default().with_protocol(generated_value::<NetworkProtocol>()),
    );
    drop(value.with_file(String::new(), Id::from("generated-id")));
    let opts = ContainerWithFileOpts::default();
    drop(value.with_file_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithFileOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithFileOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithFileOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(ContainerWithFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_files(
        String::new(),
        generated_value::<Vec<dagger_sdk::IdInput<File>>>(),
    ));
    let opts = ContainerWithFilesOpts::default();
    drop(value.with_files_opts(
        String::new(),
        generated_value::<Vec<dagger_sdk::IdInput<File>>>(),
        &opts,
    ));
    let _ = &opts.expand;
    drop(ContainerWithFilesOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithFilesOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithFilesOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(ContainerWithFilesOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_label(String::new(), String::new()));
    drop(value.with_mounted_cache(Id::from("generated-id"), String::new()));
    let opts = ContainerWithMountedCacheOpts::default();
    drop(value.with_mounted_cache_opts(Id::from("generated-id"), String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedCacheOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithMountedCacheOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithMountedCacheOpts::default().with_owner(String::new()));
    let _ = &opts.sharing;
    drop(
        ContainerWithMountedCacheOpts::default()
            .with_sharing(generated_value::<CacheSharingMode>()),
    );
    let _ = &opts.source;
    drop(ContainerWithMountedCacheOpts::default().with_source(Id::from("generated-id").into()));
    drop(value.with_mounted_directory(String::new(), Id::from("generated-id")));
    let opts = ContainerWithMountedDirectoryOpts::default();
    drop(value.with_mounted_directory_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedDirectoryOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(
        ContainerWithMountedDirectoryOpts::default().with_inherit_owner(generated_value::<bool>()),
    );
    let _ = &opts.owner;
    drop(ContainerWithMountedDirectoryOpts::default().with_owner(String::new()));
    let _ = &opts.read_only;
    drop(ContainerWithMountedDirectoryOpts::default().with_read_only(generated_value::<bool>()));
    drop(value.with_mounted_file(String::new(), Id::from("generated-id")));
    let opts = ContainerWithMountedFileOpts::default();
    drop(value.with_mounted_file_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedFileOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithMountedFileOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithMountedFileOpts::default().with_owner(String::new()));
    drop(value.with_mounted_secret(String::new(), Id::from("generated-id")));
    let opts = ContainerWithMountedSecretOpts::default();
    drop(value.with_mounted_secret_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedSecretOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithMountedSecretOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.mode;
    drop(ContainerWithMountedSecretOpts::default().with_mode(generated_value::<i64>()));
    let _ = &opts.owner;
    drop(ContainerWithMountedSecretOpts::default().with_owner(String::new()));
    drop(value.with_mounted_temp(String::new()));
    let opts = ContainerWithMountedTempOpts::default();
    drop(value.with_mounted_temp_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedTempOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.size;
    drop(ContainerWithMountedTempOpts::default().with_size(generated_value::<i64>()));
    drop(value.with_mounted_volume(String::new(), Id::from("generated-id")));
    let opts = ContainerWithMountedVolumeOpts::default();
    drop(value.with_mounted_volume_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithMountedVolumeOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.read_only;
    drop(ContainerWithMountedVolumeOpts::default().with_read_only(generated_value::<bool>()));
    drop(value.with_new_file(String::new(), String::new()));
    let opts = ContainerWithNewFileOpts::default();
    drop(value.with_new_file_opts(String::new(), String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithNewFileOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithNewFileOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithNewFileOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(ContainerWithNewFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_registry_auth(String::new(), Id::from("generated-id"), String::new()));
    drop(value.with_rootfs(Id::from("generated-id")));
    drop(value.with_secret_variable(String::new(), Id::from("generated-id")));
    drop(value.with_service_binding(String::new(), Id::from("generated-id")));
    drop(value.with_symlink(String::new(), String::new()));
    let opts = ContainerWithSymlinkOpts::default();
    drop(value.with_symlink_opts(String::new(), String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithSymlinkOpts::default().with_expand(generated_value::<bool>()));
    drop(value.with_unix_socket(String::new(), Id::from("generated-id")));
    let opts = ContainerWithUnixSocketOpts::default();
    drop(value.with_unix_socket_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.expand;
    drop(ContainerWithUnixSocketOpts::default().with_expand(generated_value::<bool>()));
    let _ = &opts.inherit_owner;
    drop(ContainerWithUnixSocketOpts::default().with_inherit_owner(generated_value::<bool>()));
    let _ = &opts.owner;
    drop(ContainerWithUnixSocketOpts::default().with_owner(String::new()));
    drop(value.with_user(String::new()));
    drop(value.with_volatile_variable(String::new(), String::new()));
    drop(value.with_workdir(String::new()));
    let opts = ContainerWithWorkdirOpts::default();
    drop(value.with_workdir_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithWorkdirOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_annotation(String::new()));
    drop(value.without_default_args());
    drop(value.without_directory(String::new()));
    let opts = ContainerWithoutDirectoryOpts::default();
    drop(value.without_directory_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithoutDirectoryOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_docker_healthcheck());
    drop(value.without_entrypoint());
    let opts = ContainerWithoutEntrypointOpts::default();
    drop(value.without_entrypoint_opts(&opts));
    let _ = &opts.keep_default_args;
    drop(
        ContainerWithoutEntrypointOpts::default().with_keep_default_args(generated_value::<bool>()),
    );
    drop(value.without_env_variable(String::new()));
    drop(value.without_exposed_port(generated_value::<i64>()));
    let opts = ContainerWithoutExposedPortOpts::default();
    drop(value.without_exposed_port_opts(generated_value::<i64>(), &opts));
    let _ = &opts.protocol;
    drop(
        ContainerWithoutExposedPortOpts::default()
            .with_protocol(generated_value::<NetworkProtocol>()),
    );
    drop(value.without_file(String::new()));
    let opts = ContainerWithoutFileOpts::default();
    drop(value.without_file_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithoutFileOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_files(generated_value::<Vec<String>>()));
    let opts = ContainerWithoutFilesOpts::default();
    drop(value.without_files_opts(generated_value::<Vec<String>>(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithoutFilesOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_label(String::new()));
    drop(value.without_mount(String::new()));
    let opts = ContainerWithoutMountOpts::default();
    drop(value.without_mount_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithoutMountOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_registry_auth(String::new()));
    drop(value.without_secret_variable(String::new()));
    drop(value.without_unix_socket(String::new()));
    let opts = ContainerWithoutUnixSocketOpts::default();
    drop(value.without_unix_socket_opts(String::new(), &opts));
    let _ = &opts.expand;
    drop(ContainerWithoutUnixSocketOpts::default().with_expand(generated_value::<bool>()));
    drop(value.without_user());
    drop(value.without_volatile_variable(String::new()));
    drop(value.without_workdir());
    drop(value.workdir());
}
#[allow(deprecated)]
fn reach_currentmodule(value: &CurrentModule) {
    drop(value.as_sdk());
    let opts = CurrentModuleAsSdkOpts::default();
    drop(value.as_sdk_opts(&opts));
    let _ = &opts.workspace;
    drop(CurrentModuleAsSdkOpts::default().with_workspace(Id::from("generated-id").into()));
    drop(value.dependencies());
    drop(value.generated_context_directory());
    drop(value.generators());
    let opts = CurrentModuleGeneratorsOpts::default();
    drop(value.generators_opts(&opts));
    let _ = &opts.include;
    drop(CurrentModuleGeneratorsOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.id());
    drop(value.name());
    drop(value.source());
    drop(value.workdir(String::new()));
    let opts = CurrentModuleWorkdirOpts::default();
    drop(value.workdir_opts(String::new(), &opts));
    let _ = &opts.exclude;
    drop(CurrentModuleWorkdirOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(CurrentModuleWorkdirOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(CurrentModuleWorkdirOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.workdir_file(String::new()));
}
#[allow(deprecated)]
fn reach_currentmoduleassdk(value: &CurrentModuleAsSdk) {
    drop(value.clients());
    drop(value.id());
    drop(value.modules());
    drop(value.name());
}
#[allow(deprecated)]
fn reach_currentmoduleassdkclient(value: &CurrentModuleAsSdkClient) {
    drop(value.id());
    drop(value.module());
    drop(value.module_source());
    drop(value.path());
    drop(value.pin());
}
#[allow(deprecated)]
fn reach_currentmoduleassdkmodule(value: &CurrentModuleAsSdkModule) {
    drop(value.id());
    drop(value.path());
}
#[allow(deprecated)]
fn reach_diffstat(value: &DiffStat) {
    drop(value.added_lines());
    drop(value.id());
    drop(value.kind());
    drop(value.old_path());
    drop(value.path());
    drop(value.removed_lines());
}
#[allow(deprecated)]
fn reach_directory(value: &Directory) {
    drop(value.as_git());
    drop(value.as_module());
    let opts = DirectoryAsModuleOpts::default();
    drop(value.as_module_opts(&opts));
    let _ = &opts.source_root_path;
    drop(DirectoryAsModuleOpts::default().with_source_root_path(String::new()));
    drop(value.as_module_source());
    let opts = DirectoryAsModuleSourceOpts::default();
    drop(value.as_module_source_opts(&opts));
    let _ = &opts.source_root_path;
    drop(DirectoryAsModuleSourceOpts::default().with_source_root_path(String::new()));
    drop(value.as_workspace());
    let opts = DirectoryAsWorkspaceOpts::default();
    drop(value.as_workspace_opts(&opts));
    let _ = &opts.cwd;
    drop(DirectoryAsWorkspaceOpts::default().with_cwd(String::new()));
    drop(value.changes(Id::from("generated-id")));
    drop(value.chown(String::new(), String::new()));
    drop(value.diff(Id::from("generated-id")));
    drop(value.digest());
    drop(value.directory(String::new()));
    drop(value.docker_build());
    let opts = DirectoryDockerBuildOpts::default();
    drop(value.docker_build_opts(&opts));
    let _ = &opts.build_args;
    drop(DirectoryDockerBuildOpts::default().with_build_args(generated_value::<Vec<BuildArg>>()));
    let _ = &opts.dockerfile;
    drop(DirectoryDockerBuildOpts::default().with_dockerfile(String::new()));
    let _ = &opts.no_init;
    drop(DirectoryDockerBuildOpts::default().with_no_init(generated_value::<bool>()));
    let _ = &opts.platform;
    drop(
        DirectoryDockerBuildOpts::default()
            .with_platform(generated_value::<dagger_sdk::Platform>()),
    );
    let _ = &opts.secrets;
    drop(
        DirectoryDockerBuildOpts::default()
            .with_secrets(generated_value::<Vec<dagger_sdk::IdInput<Secret>>>()),
    );
    let _ = &opts.ssh;
    drop(DirectoryDockerBuildOpts::default().with_ssh(Id::from("generated-id").into()));
    let _ = &opts.target;
    drop(DirectoryDockerBuildOpts::default().with_target(String::new()));
    drop(value.entries());
    let opts = DirectoryEntriesOpts::default();
    drop(value.entries_opts(&opts));
    let _ = &opts.path;
    drop(DirectoryEntriesOpts::default().with_path(String::new()));
    drop(value.exists(String::new()));
    let opts = DirectoryExistsOpts::default();
    drop(value.exists_opts(String::new(), &opts));
    let _ = &opts.do_not_follow_symlinks;
    drop(DirectoryExistsOpts::default().with_do_not_follow_symlinks(generated_value::<bool>()));
    let _ = &opts.expected_type;
    drop(DirectoryExistsOpts::default().with_expected_type(generated_value::<ExistsType>()));
    drop(value.export(String::new()));
    let opts = DirectoryExportOpts::default();
    drop(value.export_opts(String::new(), &opts));
    let _ = &opts.wipe;
    drop(DirectoryExportOpts::default().with_wipe(generated_value::<bool>()));
    drop(value.file(String::new()));
    drop(value.filter());
    let opts = DirectoryFilterOpts::default();
    drop(value.filter_opts(&opts));
    let _ = &opts.exclude;
    drop(DirectoryFilterOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(DirectoryFilterOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(DirectoryFilterOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.find_up(String::new(), String::new()));
    drop(value.glob(String::new()));
    drop(value.id());
    drop(value.name());
    drop(value.search(String::new()));
    let opts = DirectorySearchOpts::default();
    drop(value.search_opts(String::new(), &opts));
    let _ = &opts.dotall;
    drop(DirectorySearchOpts::default().with_dotall(generated_value::<bool>()));
    let _ = &opts.files_only;
    drop(DirectorySearchOpts::default().with_files_only(generated_value::<bool>()));
    let _ = &opts.globs;
    drop(DirectorySearchOpts::default().with_globs(generated_value::<Vec<String>>()));
    let _ = &opts.insensitive;
    drop(DirectorySearchOpts::default().with_insensitive(generated_value::<bool>()));
    let _ = &opts.limit;
    drop(DirectorySearchOpts::default().with_limit(generated_value::<i64>()));
    let _ = &opts.literal;
    drop(DirectorySearchOpts::default().with_literal(generated_value::<bool>()));
    let _ = &opts.multiline;
    drop(DirectorySearchOpts::default().with_multiline(generated_value::<bool>()));
    let _ = &opts.paths;
    drop(DirectorySearchOpts::default().with_paths(generated_value::<Vec<String>>()));
    let _ = &opts.skip_hidden;
    drop(DirectorySearchOpts::default().with_skip_hidden(generated_value::<bool>()));
    let _ = &opts.skip_ignored;
    drop(DirectorySearchOpts::default().with_skip_ignored(generated_value::<bool>()));
    drop(value.stat(String::new()));
    let opts = DirectoryStatOpts::default();
    drop(value.stat_opts(String::new(), &opts));
    let _ = &opts.do_not_follow_symlinks;
    drop(DirectoryStatOpts::default().with_do_not_follow_symlinks(generated_value::<bool>()));
    drop(value.sync());
    drop(value.terminal());
    let opts = DirectoryTerminalOpts::default();
    drop(value.terminal_opts(&opts));
    let _ = &opts.cmd;
    drop(DirectoryTerminalOpts::default().with_cmd(generated_value::<Vec<String>>()));
    let _ = &opts.container;
    drop(DirectoryTerminalOpts::default().with_container(Id::from("generated-id").into()));
    let _ = &opts.experimental_privileged_nesting;
    drop(
        DirectoryTerminalOpts::default()
            .with_experimental_privileged_nesting(generated_value::<bool>()),
    );
    let _ = &opts.insecure_root_capabilities;
    drop(
        DirectoryTerminalOpts::default().with_insecure_root_capabilities(generated_value::<bool>()),
    );
    drop(value.with_changes(Id::from("generated-id")));
    drop(value.with_directory(String::new(), Id::from("generated-id")));
    let opts = DirectoryWithDirectoryOpts::default();
    drop(value.with_directory_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.exclude;
    drop(DirectoryWithDirectoryOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(DirectoryWithDirectoryOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(DirectoryWithDirectoryOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.owner;
    drop(DirectoryWithDirectoryOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(DirectoryWithDirectoryOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_error(String::new()));
    drop(value.with_file(String::new(), Id::from("generated-id")));
    let opts = DirectoryWithFileOpts::default();
    drop(value.with_file_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.owner;
    drop(DirectoryWithFileOpts::default().with_owner(String::new()));
    let _ = &opts.permissions;
    drop(DirectoryWithFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_files(
        String::new(),
        generated_value::<Vec<dagger_sdk::IdInput<File>>>(),
    ));
    let opts = DirectoryWithFilesOpts::default();
    drop(value.with_files_opts(
        String::new(),
        generated_value::<Vec<dagger_sdk::IdInput<File>>>(),
        &opts,
    ));
    let _ = &opts.permissions;
    drop(DirectoryWithFilesOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_new_directory(String::new()));
    let opts = DirectoryWithNewDirectoryOpts::default();
    drop(value.with_new_directory_opts(String::new(), &opts));
    let _ = &opts.permissions;
    drop(DirectoryWithNewDirectoryOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_new_file(String::new(), String::new()));
    let opts = DirectoryWithNewFileOpts::default();
    drop(value.with_new_file_opts(String::new(), String::new(), &opts));
    let _ = &opts.permissions;
    drop(DirectoryWithNewFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_patch(String::new()));
    let opts = DirectoryWithPatchOpts::default();
    drop(value.with_patch_opts(String::new(), &opts));
    let _ = &opts.on_conflict;
    drop(DirectoryWithPatchOpts::default().with_on_conflict(generated_value::<PatchConflict>()));
    drop(value.with_patch_file(Id::from("generated-id")));
    let opts = DirectoryWithPatchFileOpts::default();
    drop(value.with_patch_file_opts(Id::from("generated-id"), &opts));
    let _ = &opts.on_conflict;
    drop(
        DirectoryWithPatchFileOpts::default().with_on_conflict(generated_value::<PatchConflict>()),
    );
    drop(value.with_symlink(String::new(), String::new()));
    drop(value.with_timestamps(generated_value::<i64>()));
    drop(value.without_directory(String::new()));
    drop(value.without_file(String::new()));
    drop(value.without_files(generated_value::<Vec<String>>()));
}
#[allow(deprecated)]
fn reach_engine(value: &Engine) {
    drop(value.clients());
    drop(value.id());
    drop(value.local_cache());
    drop(value.name());
}
#[allow(deprecated)]
fn reach_enginecache(value: &EngineCache) {
    drop(value.entry_set());
    let opts = EngineCacheEntrySetOpts::default();
    drop(value.entry_set_opts(&opts));
    let _ = &opts.key;
    drop(EngineCacheEntrySetOpts::default().with_key(String::new()));
    drop(value.id());
    drop(value.max_used_space());
    drop(value.min_free_space());
    drop(value.prune());
    let opts = EngineCachePruneOpts::default();
    drop(value.prune_opts(&opts));
    let _ = &opts.max_used_space;
    drop(EngineCachePruneOpts::default().with_max_used_space(String::new()));
    let _ = &opts.min_free_space;
    drop(EngineCachePruneOpts::default().with_min_free_space(String::new()));
    let _ = &opts.reserved_space;
    drop(EngineCachePruneOpts::default().with_reserved_space(String::new()));
    let _ = &opts.target_space;
    drop(EngineCachePruneOpts::default().with_target_space(String::new()));
    let _ = &opts.use_default_policy;
    drop(EngineCachePruneOpts::default().with_use_default_policy(generated_value::<bool>()));
    drop(value.reserved_space());
    drop(value.target_space());
}
#[allow(deprecated)]
fn reach_enginecacheentry(value: &EngineCacheEntry) {
    drop(value.actively_used());
    drop(value.created_time_unix_nano());
    drop(value.dagql_call());
    drop(value.description());
    drop(value.disk_space_bytes());
    drop(value.id());
    drop(value.most_recent_use_time_unix_nano());
    drop(value.record_type());
    drop(value.record_types());
}
#[allow(deprecated)]
fn reach_enginecacheentryset(value: &EngineCacheEntrySet) {
    drop(value.disk_space_bytes());
    drop(value.entries());
    drop(value.entry_count());
    drop(value.id());
}
#[allow(deprecated)]
fn reach_enumtypedef(value: &EnumTypeDef) {
    drop(value.description());
    drop(value.id());
    drop(value.members());
    drop(value.name());
    drop(value.source_map());
    drop(value.source_module_name());
    drop(value.values());
}
#[allow(deprecated)]
fn reach_enumvaluetypedef(value: &EnumValueTypeDef) {
    drop(value.deprecated());
    drop(value.description());
    drop(value.id());
    drop(value.name());
    drop(value.source_map());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_envfile(value: &EnvFile) {
    drop(value.as_file());
    drop(value.exists(String::new()));
    drop(value.get(String::new()));
    let opts = EnvFileGetOpts::default();
    drop(value.get_opts(String::new(), &opts));
    let _ = &opts.raw;
    drop(EnvFileGetOpts::default().with_raw(generated_value::<bool>()));
    drop(value.id());
    drop(value.namespace(String::new()));
    drop(value.variables());
    let opts = EnvFileVariablesOpts::default();
    drop(value.variables_opts(&opts));
    let _ = &opts.raw;
    drop(EnvFileVariablesOpts::default().with_raw(generated_value::<bool>()));
    drop(value.with_variable(String::new(), String::new()));
    drop(value.without_variable(String::new()));
}
#[allow(deprecated)]
fn reach_envvariable(value: &EnvVariable) {
    drop(value.id());
    drop(value.name());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_error(value: &Error) {
    drop(value.id());
    drop(value.message());
    drop(value.values());
    drop(value.with_value(String::new(), generated_value::<dagger_sdk::Json>()));
}
#[allow(deprecated)]
fn reach_errorvalue(value: &ErrorValue) {
    drop(value.id());
    drop(value.name());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_exportableclient(value: &ExportableClient) {
    drop(value.export(String::new()));
    drop(value.id());
}
#[allow(deprecated)]
fn reach_exportable_trait<T: Exportable>(value: &T) {
    drop(value.export(String::new()));
    drop(value.id());
}
#[allow(deprecated)]
fn reach_fieldtypedef(value: &FieldTypeDef) {
    drop(value.deprecated());
    drop(value.description());
    drop(value.id());
    drop(value.name());
    drop(value.source_map());
    drop(value.type_def());
}
#[allow(deprecated)]
fn reach_file(value: &File) {
    drop(value.as_env_file());
    let opts = FileAsEnvFileOpts::default();
    drop(value.as_env_file_opts(&opts));
    let _ = &opts.expand;
    drop(FileAsEnvFileOpts::default().with_expand(generated_value::<bool>()));
    drop(value.as_json());
    drop(value.chown(String::new()));
    drop(value.contents());
    let opts = FileContentsOpts::default();
    drop(value.contents_opts(&opts));
    let _ = &opts.limit_lines;
    drop(FileContentsOpts::default().with_limit_lines(generated_value::<i64>()));
    let _ = &opts.offset_lines;
    drop(FileContentsOpts::default().with_offset_lines(generated_value::<i64>()));
    drop(value.digest());
    let opts = FileDigestOpts::default();
    drop(value.digest_opts(&opts));
    let _ = &opts.exclude_metadata;
    drop(FileDigestOpts::default().with_exclude_metadata(generated_value::<bool>()));
    drop(value.export(String::new()));
    let opts = FileExportOpts::default();
    drop(value.export_opts(String::new(), &opts));
    let _ = &opts.allow_parent_dir_path;
    drop(FileExportOpts::default().with_allow_parent_dir_path(generated_value::<bool>()));
    drop(value.id());
    drop(value.name());
    drop(value.search(String::new()));
    let opts = FileSearchOpts::default();
    drop(value.search_opts(String::new(), &opts));
    let _ = &opts.dotall;
    drop(FileSearchOpts::default().with_dotall(generated_value::<bool>()));
    let _ = &opts.files_only;
    drop(FileSearchOpts::default().with_files_only(generated_value::<bool>()));
    let _ = &opts.globs;
    drop(FileSearchOpts::default().with_globs(generated_value::<Vec<String>>()));
    let _ = &opts.insensitive;
    drop(FileSearchOpts::default().with_insensitive(generated_value::<bool>()));
    let _ = &opts.limit;
    drop(FileSearchOpts::default().with_limit(generated_value::<i64>()));
    let _ = &opts.literal;
    drop(FileSearchOpts::default().with_literal(generated_value::<bool>()));
    let _ = &opts.multiline;
    drop(FileSearchOpts::default().with_multiline(generated_value::<bool>()));
    let _ = &opts.paths;
    drop(FileSearchOpts::default().with_paths(generated_value::<Vec<String>>()));
    let _ = &opts.skip_hidden;
    drop(FileSearchOpts::default().with_skip_hidden(generated_value::<bool>()));
    let _ = &opts.skip_ignored;
    drop(FileSearchOpts::default().with_skip_ignored(generated_value::<bool>()));
    drop(value.size());
    drop(value.stat());
    drop(value.sync());
    drop(value.with_name(String::new()));
    drop(value.with_replaced(String::new(), String::new()));
    let opts = FileWithReplacedOpts::default();
    drop(value.with_replaced_opts(String::new(), String::new(), &opts));
    let _ = &opts.all;
    drop(FileWithReplacedOpts::default().with_all(generated_value::<bool>()));
    let _ = &opts.first_from;
    drop(FileWithReplacedOpts::default().with_first_from(generated_value::<i64>()));
    drop(value.with_timestamps(generated_value::<i64>()));
}
#[allow(deprecated)]
fn reach_function(value: &Function) {
    drop(value.args());
    drop(value.deprecated());
    drop(value.description());
    drop(value.id());
    drop(value.name());
    drop(value.return_type());
    drop(value.source_map());
    drop(value.source_module_name());
    drop(value.with_arg(String::new(), Id::from("generated-id")));
    let opts = FunctionWithArgOpts::default();
    drop(value.with_arg_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.default_address;
    drop(FunctionWithArgOpts::default().with_default_address(String::new()));
    let _ = &opts.default_path;
    drop(FunctionWithArgOpts::default().with_default_path(String::new()));
    let _ = &opts.default_value;
    drop(FunctionWithArgOpts::default().with_default_value(generated_value::<dagger_sdk::Json>()));
    let _ = &opts.deprecated;
    drop(FunctionWithArgOpts::default().with_deprecated(String::new()));
    let _ = &opts.description;
    drop(FunctionWithArgOpts::default().with_description(String::new()));
    let _ = &opts.ignore;
    drop(FunctionWithArgOpts::default().with_ignore(generated_value::<Vec<String>>()));
    let _ = &opts.source_map;
    drop(FunctionWithArgOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_cache_policy(generated_value::<FunctionCachePolicy>()));
    let opts = FunctionWithCachePolicyOpts::default();
    drop(value.with_cache_policy_opts(generated_value::<FunctionCachePolicy>(), &opts));
    let _ = &opts.time_to_live;
    drop(FunctionWithCachePolicyOpts::default().with_time_to_live(String::new()));
    drop(value.with_check());
    drop(value.with_deprecated());
    let opts = FunctionWithDeprecatedOpts::default();
    drop(value.with_deprecated_opts(&opts));
    let _ = &opts.reason;
    drop(FunctionWithDeprecatedOpts::default().with_reason(String::new()));
    drop(value.with_description(String::new()));
    drop(value.with_generator());
    drop(value.with_source_map(Id::from("generated-id")));
    drop(value.with_up());
}
#[allow(deprecated)]
fn reach_functionarg(value: &FunctionArg) {
    drop(value.default_address());
    drop(value.default_path());
    drop(value.default_value());
    drop(value.deprecated());
    drop(value.description());
    drop(value.id());
    drop(value.ignore());
    drop(value.name());
    drop(value.source_map());
    drop(value.type_def());
}
#[allow(deprecated)]
fn reach_functioncall(value: &FunctionCall) {
    drop(value.id());
    drop(value.input_args());
    drop(value.name());
    drop(value.parent());
    drop(value.parent_name());
    drop(value.return_error(Id::from("generated-id")));
    drop(value.return_value(generated_value::<dagger_sdk::Json>()));
}
#[allow(deprecated)]
fn reach_functioncallargvalue(value: &FunctionCallArgValue) {
    drop(value.id());
    drop(value.name());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_generatedcode(value: &GeneratedCode) {
    drop(value.code());
    drop(value.id());
    drop(value.vcs_generated_paths());
    drop(value.vcs_ignored_paths());
    drop(value.with_vcs_generated_paths(generated_value::<Vec<String>>()));
    drop(value.with_vcs_ignored_paths(generated_value::<Vec<String>>()));
}
#[allow(deprecated)]
fn reach_generator(value: &Generator) {
    drop(value.changes());
    drop(value.completed());
    drop(value.description());
    drop(value.id());
    drop(value.is_empty());
    drop(value.name());
    drop(value.original_module());
    drop(value.path());
    drop(value.run());
}
#[allow(deprecated)]
fn reach_generatorgroup(value: &GeneratorGroup) {
    drop(value.changes());
    let opts = GeneratorGroupChangesOpts::default();
    drop(value.changes_opts(&opts));
    let _ = &opts.on_conflict;
    drop(
        GeneratorGroupChangesOpts::default()
            .with_on_conflict(generated_value::<ChangesetsMergeConflict>()),
    );
    drop(value.id());
    drop(value.is_empty());
    drop(value.list());
    drop(value.load_failures());
    drop(value.run());
}
#[allow(deprecated)]
fn reach_gitref(value: &GitRef) {
    drop(value.as_workspace());
    let opts = GitRefAsWorkspaceOpts::default();
    drop(value.as_workspace_opts(&opts));
    let _ = &opts.cwd;
    drop(GitRefAsWorkspaceOpts::default().with_cwd(String::new()));
    drop(value.commit());
    drop(value.common_ancestor(Id::from("generated-id")));
    drop(value.id());
    drop(value.r#ref());
    drop(value.tree());
    let opts = GitRefTreeOpts::default();
    drop(value.tree_opts(&opts));
    let _ = &opts.depth;
    drop(GitRefTreeOpts::default().with_depth(generated_value::<i64>()));
    let _ = &opts.discard_git_dir;
    drop(GitRefTreeOpts::default().with_discard_git_dir(generated_value::<bool>()));
    let _ = &opts.include_tags;
    drop(GitRefTreeOpts::default().with_include_tags(generated_value::<bool>()));
}
#[allow(deprecated)]
fn reach_gitrepository(value: &GitRepository) {
    drop(value.as_workspace());
    let opts = GitRepositoryAsWorkspaceOpts::default();
    drop(value.as_workspace_opts(&opts));
    let _ = &opts.cwd;
    drop(GitRepositoryAsWorkspaceOpts::default().with_cwd(String::new()));
    drop(value.branch(String::new()));
    drop(value.branches());
    let opts = GitRepositoryBranchesOpts::default();
    drop(value.branches_opts(&opts));
    let _ = &opts.patterns;
    drop(GitRepositoryBranchesOpts::default().with_patterns(generated_value::<Vec<String>>()));
    drop(value.commit(String::new()));
    drop(value.head());
    drop(value.id());
    drop(value.latest_version());
    drop(value.r#ref(String::new()));
    drop(value.tag(String::new()));
    drop(value.tags());
    let opts = GitRepositoryTagsOpts::default();
    drop(value.tags_opts(&opts));
    let _ = &opts.patterns;
    drop(GitRepositoryTagsOpts::default().with_patterns(generated_value::<Vec<String>>()));
    drop(value.uncommitted());
    drop(value.url());
}
#[allow(deprecated)]
fn reach_httpstate(value: &HttpState) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_healthcheckconfig(value: &HealthcheckConfig) {
    drop(value.args());
    drop(value.id());
    drop(value.interval());
    drop(value.retries());
    drop(value.shell());
    drop(value.start_interval());
    drop(value.start_period());
    drop(value.timeout());
}
#[allow(deprecated)]
fn reach_host(value: &Host) {
    drop(value.container_image(String::new()));
    drop(value.directory(String::new()));
    let opts = HostDirectoryOpts::default();
    drop(value.directory_opts(String::new(), &opts));
    let _ = &opts.exclude;
    drop(HostDirectoryOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(HostDirectoryOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(HostDirectoryOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_cache;
    drop(HostDirectoryOpts::default().with_no_cache(generated_value::<bool>()));
    drop(value.file(String::new()));
    let opts = HostFileOpts::default();
    drop(value.file_opts(String::new(), &opts));
    let _ = &opts.no_cache;
    drop(HostFileOpts::default().with_no_cache(generated_value::<bool>()));
    drop(value.find_up(String::new()));
    let opts = HostFindUpOpts::default();
    drop(value.find_up_opts(String::new(), &opts));
    let _ = &opts.no_cache;
    drop(HostFindUpOpts::default().with_no_cache(generated_value::<bool>()));
    drop(value.id());
    drop(value.service(generated_value::<Vec<PortForward>>()));
    let opts = HostServiceOpts::default();
    drop(value.service_opts(generated_value::<Vec<PortForward>>(), &opts));
    let _ = &opts.host;
    drop(HostServiceOpts::default().with_host(String::new()));
    drop(value.tunnel(Id::from("generated-id")));
    let opts = HostTunnelOpts::default();
    drop(value.tunnel_opts(Id::from("generated-id"), &opts));
    let _ = &opts.native;
    drop(HostTunnelOpts::default().with_native(generated_value::<bool>()));
    let _ = &opts.ports;
    drop(HostTunnelOpts::default().with_ports(generated_value::<Vec<PortForward>>()));
    drop(value.unix_socket(String::new()));
}
#[allow(deprecated)]
fn reach_inputtypedef(value: &InputTypeDef) {
    drop(value.fields());
    drop(value.id());
    drop(value.name());
}
#[allow(deprecated)]
fn reach_interfacetypedef(value: &InterfaceTypeDef) {
    drop(value.description());
    drop(value.functions());
    drop(value.id());
    drop(value.name());
    drop(value.source_map());
    drop(value.source_module_name());
}
#[allow(deprecated)]
fn reach_jsonvalue(value: &JsonValue) {
    drop(value.as_array());
    drop(value.as_boolean());
    drop(value.as_integer());
    drop(value.as_string());
    drop(value.contents());
    let opts = JsonValueContentsOpts::default();
    drop(value.contents_opts(&opts));
    let _ = &opts.indent;
    drop(JsonValueContentsOpts::default().with_indent(String::new()));
    let _ = &opts.pretty;
    drop(JsonValueContentsOpts::default().with_pretty(generated_value::<bool>()));
    drop(value.field(generated_value::<Vec<String>>()));
    drop(value.fields());
    drop(value.id());
    drop(value.new_boolean(generated_value::<bool>()));
    drop(value.new_integer(generated_value::<i64>()));
    drop(value.new_string(String::new()));
    drop(value.with_contents(generated_value::<dagger_sdk::Json>()));
    drop(value.with_field(generated_value::<Vec<String>>(), Id::from("generated-id")));
}
#[allow(deprecated)]
fn reach_llm(value: &Llm) {
    drop(value.context_tokens());
    drop(value.context_window());
    drop(value.fork(String::new()));
    drop(value.has_pending());
    drop(value.id());
    drop(value.last_reply());
    drop(value.r#loop());
    let opts = LlmLoopOpts::default();
    drop(value.loop_opts(&opts));
    let _ = &opts.max_steps;
    drop(LlmLoopOpts::default().with_max_steps(generated_value::<i64>()));
    let _ = &opts.max_tokens;
    drop(LlmLoopOpts::default().with_max_tokens(generated_value::<i64>()));
    drop(value.messages());
    drop(value.model());
    drop(value.portable_id());
    drop(value.provider());
    drop(value.replay());
    drop(value.step());
    let opts = LlmStepOpts::default();
    drop(value.step_opts(&opts));
    let _ = &opts.max_tokens;
    drop(LlmStepOpts::default().with_max_tokens(generated_value::<i64>()));
    drop(value.sync());
    drop(value.token_usage());
    drop(value.tools());
    drop(value.transcript());
    drop(value.with_mcp_server(String::new(), Id::from("generated-id")));
    drop(value.with_model(String::new()));
    let opts = LlmWithModelOpts::default();
    drop(value.with_model_opts(String::new(), &opts));
    let _ = &opts.provider;
    drop(LlmWithModelOpts::default().with_provider(String::new()));
    drop(value.with_prompt(String::new()));
    drop(value.with_prompt_file(Id::from("generated-id")));
    drop(value.with_response(generated_value::<Vec<LlmContentBlockInput>>()));
    let opts = LlmWithResponseOpts::default();
    drop(value.with_response_opts(generated_value::<Vec<LlmContentBlockInput>>(), &opts));
    let _ = &opts.cached_token_reads;
    drop(LlmWithResponseOpts::default().with_cached_token_reads(generated_value::<i64>()));
    let _ = &opts.cached_token_writes;
    drop(LlmWithResponseOpts::default().with_cached_token_writes(generated_value::<i64>()));
    let _ = &opts.input_tokens;
    drop(LlmWithResponseOpts::default().with_input_tokens(generated_value::<i64>()));
    let _ = &opts.output_tokens;
    drop(LlmWithResponseOpts::default().with_output_tokens(generated_value::<i64>()));
    let _ = &opts.total_tokens;
    drop(LlmWithResponseOpts::default().with_total_tokens(generated_value::<i64>()));
    drop(value.with_system_prompt(String::new()));
    drop(value.with_tool_result(String::new(), String::new(), generated_value::<bool>()));
    drop(value.with_tools(Id::from("generated-id")));
    let opts = LlmWithToolsOpts::default();
    drop(value.with_tools_opts(Id::from("generated-id"), &opts));
    let _ = &opts.except;
    drop(LlmWithToolsOpts::default().with_except(generated_value::<Vec<String>>()));
    drop(value.with_workspace(Id::from("generated-id")));
    drop(value.without_default_system_prompt());
    drop(value.without_message_history());
    drop(value.without_system_prompts());
    drop(value.workspace());
}
#[allow(deprecated)]
fn reach_llmcontentblock(value: &LlmContentBlock) {
    drop(value.arguments());
    drop(value.call_id());
    drop(value.errored());
    drop(value.id());
    drop(value.kind());
    drop(value.signature());
    drop(value.text());
    drop(value.tool_name());
}
#[allow(deprecated)]
fn reach_llmcontentblockinput_input(value: &LlmContentBlockInput) {
    drop(LlmContentBlockInput::new(generated_value::<
        LlmContentBlockKind,
    >()));
    let _ = &value.arguments;
    drop(
        value
            .clone()
            .with_arguments(generated_value::<dagger_sdk::Json>()),
    );
    let _ = &value.call_id;
    drop(value.clone().with_call_id(String::new()));
    let _ = &value.errored;
    drop(value.clone().with_errored(generated_value::<bool>()));
    let _ = &value.kind;
    let _ = &value.signature;
    drop(value.clone().with_signature(String::new()));
    let _ = &value.text;
    drop(value.clone().with_text(String::new()));
    let _ = &value.tool_name;
    drop(value.clone().with_tool_name(String::new()));
}
#[allow(deprecated)]
fn reach_llmmessage(value: &LlmMessage) {
    drop(value.content());
    drop(value.id());
    drop(value.role());
    drop(value.token_usage());
}
#[allow(deprecated)]
fn reach_llmtokenusage(value: &LlmTokenUsage) {
    drop(value.cached_token_reads());
    drop(value.cached_token_writes());
    drop(value.id());
    drop(value.input_tokens());
    drop(value.output_tokens());
    drop(value.total_tokens());
}
#[allow(deprecated)]
fn reach_label(value: &Label) {
    drop(value.id());
    drop(value.name());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_listtypedef(value: &ListTypeDef) {
    drop(value.element_type_def());
    drop(value.id());
}
#[allow(deprecated)]
fn reach_module(value: &Module) {
    drop(value.check(String::new()));
    drop(value.checks());
    let opts = ModuleChecksOpts::default();
    drop(value.checks_opts(&opts));
    let _ = &opts.include;
    drop(ModuleChecksOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_generate;
    drop(ModuleChecksOpts::default().with_no_generate(generated_value::<bool>()));
    drop(value.dependencies());
    drop(value.description());
    drop(value.enums());
    drop(value.generated_context_directory());
    drop(value.generator(String::new()));
    drop(value.generators());
    let opts = ModuleGeneratorsOpts::default();
    drop(value.generators_opts(&opts));
    let _ = &opts.include;
    drop(ModuleGeneratorsOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.id());
    drop(value.interfaces());
    drop(value.introspection_schema_json());
    drop(value.name());
    drop(value.objects());
    drop(value.runtime());
    drop(value.sdk());
    drop(value.serve());
    let opts = ModuleServeOpts::default();
    drop(value.serve_opts(&opts));
    let _ = &opts.entrypoint;
    drop(ModuleServeOpts::default().with_entrypoint(generated_value::<bool>()));
    let _ = &opts.include_dependencies;
    drop(ModuleServeOpts::default().with_include_dependencies(generated_value::<bool>()));
    drop(value.services());
    let opts = ModuleServicesOpts::default();
    drop(value.services_opts(&opts));
    let _ = &opts.include;
    drop(ModuleServicesOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.source());
    drop(value.sync());
    drop(value.user_defaults());
    drop(value.with_description(String::new()));
    drop(value.with_enum(Id::from("generated-id")));
    drop(value.with_interface(Id::from("generated-id")));
    drop(value.with_object(Id::from("generated-id")));
}
#[allow(deprecated)]
fn reach_moduleconfigclient(value: &ModuleConfigClient) {
    drop(value.directory());
    drop(value.generator());
    drop(value.id());
}
#[allow(deprecated)]
fn reach_modulesource(value: &ModuleSource) {
    drop(value.as_module());
    drop(value.as_string());
    drop(value.blueprint());
    drop(value.client_schema_introspection_json());
    drop(value.clone_ref());
    drop(value.commit());
    drop(value.config_clients());
    drop(value.config_exists());
    drop(value.context_directory());
    drop(value.dependencies());
    drop(value.digest());
    drop(value.directory(String::new()));
    drop(value.engine_version());
    drop(value.generate_local_dependencies(Id::from("generated-id")));
    drop(value.generated_context_changeset());
    drop(value.generated_context_directory());
    drop(value.html_repo_url());
    drop(value.html_url());
    drop(value.id());
    drop(value.introspection_schema_json());
    drop(value.kind());
    drop(value.local_context_directory_path());
    drop(value.module_name());
    drop(value.module_original_name());
    drop(value.original_subpath());
    drop(value.pin());
    drop(value.repo_root_path());
    drop(value.sdk());
    drop(value.source_root_subpath());
    drop(value.source_subpath());
    drop(value.sync());
    drop(value.toolchains());
    drop(value.updated_config_directory());
    drop(value.user_defaults());
    drop(value.version());
    drop(value.with_blueprint(Id::from("generated-id")));
    drop(value.with_client(String::new(), String::new()));
    drop(value.with_dependencies(generated_value::<Vec<dagger_sdk::IdInput<ModuleSource>>>()));
    drop(value.with_engine_version(String::new()));
    drop(
        value.with_experimental_features(generated_value::<Vec<ModuleSourceExperimentalFeature>>()),
    );
    drop(value.with_includes(generated_value::<Vec<String>>()));
    drop(value.with_name(String::new()));
    drop(value.with_sdk(String::new()));
    drop(value.with_source_subpath(String::new()));
    drop(value.with_toolchains(generated_value::<Vec<dagger_sdk::IdInput<ModuleSource>>>()));
    drop(value.with_update_blueprint());
    drop(value.with_update_dependencies(generated_value::<Vec<String>>()));
    drop(value.with_update_toolchains(generated_value::<Vec<String>>()));
    drop(value.with_updated_clients(generated_value::<Vec<String>>()));
    drop(value.without_blueprint());
    drop(value.without_client(String::new()));
    drop(value.without_dependencies(generated_value::<Vec<String>>()));
    drop(
        value.without_experimental_features(
            generated_value::<Vec<ModuleSourceExperimentalFeature>>(),
        ),
    );
    drop(value.without_toolchains(generated_value::<Vec<String>>()));
}
#[allow(deprecated)]
fn reach_nodeclient(value: &NodeClient) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_node_trait<T: Node>(value: &T) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_objecttypedef(value: &ObjectTypeDef) {
    drop(value.constructor());
    drop(value.deprecated());
    drop(value.description());
    drop(value.fields());
    drop(value.functions());
    drop(value.id());
    drop(value.name());
    drop(value.source_map());
    drop(value.source_module_name());
}
#[allow(deprecated)]
fn reach_pipelinelabel_input(value: &PipelineLabel) {
    drop(PipelineLabel::new(String::new(), String::new()));
    let _ = &value.name;
    let _ = &value.value;
}
#[allow(deprecated)]
fn reach_port(value: &Port) {
    drop(value.description());
    drop(value.experimental_skip_healthcheck());
    drop(value.id());
    drop(value.port());
    drop(value.protocol());
}
#[allow(deprecated)]
fn reach_portforward_input(value: &PortForward) {
    drop(PortForward::new(generated_value::<i64>()));
    let _ = &value.backend;
    let _ = &value.frontend;
    drop(value.clone().with_frontend(generated_value::<i64>()));
    let _ = &value.protocol;
    drop(
        value
            .clone()
            .with_protocol(generated_value::<NetworkProtocol>()),
    );
}
#[allow(deprecated)]
fn reach_query(value: &Query) {
    drop(value.address(String::new()));
    drop(value.cache_volume(String::new()));
    let opts = QueryCacheVolumeOpts::default();
    drop(value.cache_volume_opts(String::new(), &opts));
    let _ = &opts.owner;
    drop(QueryCacheVolumeOpts::default().with_owner(String::new()));
    let _ = &opts.sharing;
    drop(QueryCacheVolumeOpts::default().with_sharing(generated_value::<CacheSharingMode>()));
    let _ = &opts.source;
    drop(QueryCacheVolumeOpts::default().with_source(Id::from("generated-id").into()));
    drop(value.changeset());
    drop(value.cloud());
    drop(value.container());
    let opts = QueryContainerOpts::default();
    drop(value.container_opts(&opts));
    let _ = &opts.platform;
    drop(QueryContainerOpts::default().with_platform(generated_value::<dagger_sdk::Platform>()));
    drop(value.current_function_call());
    drop(value.current_module());
    drop(value.current_node());
    drop(value.current_type_defs());
    let opts = QueryCurrentTypeDefsOpts::default();
    drop(value.current_type_defs_opts(&opts));
    let _ = &opts.hide_core;
    drop(QueryCurrentTypeDefsOpts::default().with_hide_core(generated_value::<bool>()));
    let _ = &opts.return_all_types;
    drop(QueryCurrentTypeDefsOpts::default().with_return_all_types(generated_value::<bool>()));
    drop(value.current_workspace());
    drop(value.default_platform());
    drop(value.directory());
    drop(value.engine());
    drop(value.engine_volume(String::new()));
    let opts = QueryEngineVolumeOpts::default();
    drop(value.engine_volume_opts(String::new(), &opts));
    let _ = &opts.subdir;
    drop(QueryEngineVolumeOpts::default().with_subdir(String::new()));
    drop(value.env_file());
    let opts = QueryEnvFileOpts::default();
    drop(value.env_file_opts(&opts));
    let _ = &opts.expand;
    drop(QueryEnvFileOpts::default().with_expand(generated_value::<bool>()));
    drop(value.error(String::new()));
    drop(value.file(String::new(), String::new()));
    let opts = QueryFileOpts::default();
    drop(value.file_opts(String::new(), String::new(), &opts));
    let _ = &opts.permissions;
    drop(QueryFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.function(String::new(), Id::from("generated-id")));
    drop(value.generated_code(Id::from("generated-id")));
    drop(value.git(String::new()));
    let opts = QueryGitOpts::default();
    drop(value.git_opts(String::new(), &opts));
    let _ = &opts.experimental_service_host;
    drop(QueryGitOpts::default().with_experimental_service_host(Id::from("generated-id").into()));
    let _ = &opts.http_auth_header;
    drop(QueryGitOpts::default().with_http_auth_header(Id::from("generated-id").into()));
    let _ = &opts.http_auth_token;
    drop(QueryGitOpts::default().with_http_auth_token(Id::from("generated-id").into()));
    let _ = &opts.http_auth_username;
    drop(QueryGitOpts::default().with_http_auth_username(String::new()));
    let _ = &opts.keep_git_dir;
    drop(QueryGitOpts::default().with_keep_git_dir(generated_value::<bool>()));
    let _ = &opts.ssh_auth_socket;
    drop(QueryGitOpts::default().with_ssh_auth_socket(Id::from("generated-id").into()));
    let _ = &opts.ssh_known_hosts;
    drop(QueryGitOpts::default().with_ssh_known_hosts(String::new()));
    drop(value.host());
    drop(value.http(String::new()));
    let opts = QueryHttpOpts::default();
    drop(value.http_opts(String::new(), &opts));
    let _ = &opts.auth_header;
    drop(QueryHttpOpts::default().with_auth_header(Id::from("generated-id").into()));
    let _ = &opts.checksum;
    drop(QueryHttpOpts::default().with_checksum(String::new()));
    let _ = &opts.experimental_service_host;
    drop(QueryHttpOpts::default().with_experimental_service_host(Id::from("generated-id").into()));
    let _ = &opts.name;
    drop(QueryHttpOpts::default().with_name(String::new()));
    let _ = &opts.permissions;
    drop(QueryHttpOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.id());
    drop(value.json());
    drop(value.llm());
    let opts = QueryLlmOpts::default();
    drop(value.llm_opts(&opts));
    let _ = &opts.model;
    drop(QueryLlmOpts::default().with_model(String::new()));
    let _ = &opts.provider;
    drop(QueryLlmOpts::default().with_provider(String::new()));
    drop(value.module());
    drop(value.module_source(String::new()));
    let opts = QueryModuleSourceOpts::default();
    drop(value.module_source_opts(String::new(), &opts));
    let _ = &opts.allow_not_exists;
    drop(QueryModuleSourceOpts::default().with_allow_not_exists(generated_value::<bool>()));
    let _ = &opts.disable_find_up;
    drop(QueryModuleSourceOpts::default().with_disable_find_up(generated_value::<bool>()));
    let _ = &opts.ref_pin;
    drop(QueryModuleSourceOpts::default().with_ref_pin(String::new()));
    let _ = &opts.require_kind;
    drop(QueryModuleSourceOpts::default().with_require_kind(generated_value::<ModuleSourceKind>()));
    drop(value.node(generated_value::<dagger_sdk::Id>()));
    drop(value.schema(generated_value::<dagger_sdk::Json>()));
    drop(value.secret(String::new()));
    let opts = QuerySecretOpts::default();
    drop(value.secret_opts(String::new(), &opts));
    let _ = &opts.cache_key;
    drop(QuerySecretOpts::default().with_cache_key(String::new()));
    drop(value.set_secret(String::new(), String::new()));
    drop(value.source_map(
        generated_value::<i64>(),
        String::new(),
        generated_value::<i64>(),
    ));
    drop(value.sshfs_volume(String::new(), Id::from("generated-id")));
    let opts = QuerySshfsVolumeOpts::default();
    drop(value.sshfs_volume_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.cache_key;
    drop(QuerySshfsVolumeOpts::default().with_cache_key(String::new()));
    let _ = &opts.experimental_service_host;
    drop(
        QuerySshfsVolumeOpts::default()
            .with_experimental_service_host(Id::from("generated-id").into()),
    );
    let _ = &opts.insecure_skip_host_key_check;
    drop(
        QuerySshfsVolumeOpts::default()
            .with_insecure_skip_host_key_check(generated_value::<bool>()),
    );
    let _ = &opts.known_hosts;
    drop(QuerySshfsVolumeOpts::default().with_known_hosts(Id::from("generated-id").into()));
    drop(value.type_def());
    drop(value.version());
}
#[allow(deprecated)]
fn reach_remotegitmirror(value: &RemoteGitMirror) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_sdkconfig(value: &SdkConfig) {
    drop(value.debug());
    drop(value.id());
    drop(value.source());
}
#[allow(deprecated)]
fn reach_scalartypedef(value: &ScalarTypeDef) {
    drop(value.description());
    drop(value.id());
    drop(value.name());
    drop(value.source_module_name());
}
#[allow(deprecated)]
fn reach_schema(value: &Schema) {
    drop(value.contents());
    drop(value.id());
    drop(value.merge(String::new(), generated_value::<dagger_sdk::Json>()));
}
#[allow(deprecated)]
fn reach_searchresult(value: &SearchResult) {
    drop(value.absolute_offset());
    drop(value.file_path());
    drop(value.id());
    drop(value.line_number());
    drop(value.matched_lines());
    drop(value.submatches());
}
#[allow(deprecated)]
fn reach_searchsubmatch(value: &SearchSubmatch) {
    drop(value.end());
    drop(value.id());
    drop(value.start());
    drop(value.text());
}
#[allow(deprecated)]
fn reach_secret(value: &Secret) {
    drop(value.id());
    drop(value.name());
    drop(value.plaintext());
    drop(value.uri());
}
#[allow(deprecated)]
fn reach_service(value: &Service) {
    drop(value.endpoint());
    let opts = ServiceEndpointOpts::default();
    drop(value.endpoint_opts(&opts));
    let _ = &opts.port;
    drop(ServiceEndpointOpts::default().with_port(generated_value::<i64>()));
    let _ = &opts.scheme;
    drop(ServiceEndpointOpts::default().with_scheme(String::new()));
    drop(value.hostname());
    drop(value.id());
    drop(value.ports());
    drop(value.start());
    drop(value.stop());
    let opts = ServiceStopOpts::default();
    drop(value.stop_opts(&opts));
    let _ = &opts.kill;
    drop(ServiceStopOpts::default().with_kill(generated_value::<bool>()));
    drop(value.sync());
    drop(value.terminal());
    let opts = ServiceTerminalOpts::default();
    drop(value.terminal_opts(&opts));
    let _ = &opts.cmd;
    drop(ServiceTerminalOpts::default().with_cmd(generated_value::<Vec<String>>()));
    drop(value.up());
    let opts = ServiceUpOpts::default();
    drop(value.up_opts(&opts));
    let _ = &opts.ports;
    drop(ServiceUpOpts::default().with_ports(generated_value::<Vec<PortForward>>()));
    let _ = &opts.random;
    drop(ServiceUpOpts::default().with_random(generated_value::<bool>()));
    drop(value.with_hostname(String::new()));
}
#[allow(deprecated)]
fn reach_socket(value: &Socket) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_sourcemap(value: &SourceMap) {
    drop(value.column());
    drop(value.filename());
    drop(value.id());
    drop(value.line());
    drop(value.module());
    drop(value.url());
}
#[allow(deprecated)]
fn reach_stat(value: &Stat) {
    drop(value.file_type());
    drop(value.id());
    drop(value.name());
    drop(value.permissions());
    drop(value.size());
}
#[allow(deprecated)]
fn reach_syncerclient(value: &SyncerClient) {
    drop(value.id());
    drop(value.sync());
}
#[allow(deprecated)]
fn reach_syncer_trait<T: Syncer>(value: &T) {
    drop(value.id());
    drop(value.sync());
}
#[allow(deprecated)]
fn reach_terminal(value: &Terminal) {
    drop(value.id());
    drop(value.sync());
}
#[allow(deprecated)]
fn reach_typedef(value: &TypeDef) {
    drop(value.as_enum());
    drop(value.as_input());
    drop(value.as_interface());
    drop(value.as_list());
    drop(value.as_object());
    drop(value.as_scalar());
    drop(value.id());
    drop(value.kind());
    drop(value.name());
    drop(value.optional());
    drop(value.with_constructor(Id::from("generated-id")));
    drop(value.with_enum(String::new()));
    let opts = TypeDefWithEnumOpts::default();
    drop(value.with_enum_opts(String::new(), &opts));
    let _ = &opts.description;
    drop(TypeDefWithEnumOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithEnumOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_enum_member(String::new()));
    let opts = TypeDefWithEnumMemberOpts::default();
    drop(value.with_enum_member_opts(String::new(), &opts));
    let _ = &opts.deprecated;
    drop(TypeDefWithEnumMemberOpts::default().with_deprecated(String::new()));
    let _ = &opts.description;
    drop(TypeDefWithEnumMemberOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithEnumMemberOpts::default().with_source_map(Id::from("generated-id").into()));
    let _ = &opts.value;
    drop(TypeDefWithEnumMemberOpts::default().with_value(String::new()));
    drop(value.with_enum_value(String::new()));
    let opts = TypeDefWithEnumValueOpts::default();
    drop(value.with_enum_value_opts(String::new(), &opts));
    let _ = &opts.deprecated;
    drop(TypeDefWithEnumValueOpts::default().with_deprecated(String::new()));
    let _ = &opts.description;
    drop(TypeDefWithEnumValueOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithEnumValueOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_field(String::new(), Id::from("generated-id")));
    let opts = TypeDefWithFieldOpts::default();
    drop(value.with_field_opts(String::new(), Id::from("generated-id"), &opts));
    let _ = &opts.deprecated;
    drop(TypeDefWithFieldOpts::default().with_deprecated(String::new()));
    let _ = &opts.description;
    drop(TypeDefWithFieldOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithFieldOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_function(Id::from("generated-id")));
    drop(value.with_interface(String::new()));
    let opts = TypeDefWithInterfaceOpts::default();
    drop(value.with_interface_opts(String::new(), &opts));
    let _ = &opts.description;
    drop(TypeDefWithInterfaceOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithInterfaceOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_kind(generated_value::<TypeDefKind>()));
    drop(value.with_list_of(Id::from("generated-id")));
    drop(value.with_object(String::new()));
    let opts = TypeDefWithObjectOpts::default();
    drop(value.with_object_opts(String::new(), &opts));
    let _ = &opts.deprecated;
    drop(TypeDefWithObjectOpts::default().with_deprecated(String::new()));
    let _ = &opts.description;
    drop(TypeDefWithObjectOpts::default().with_description(String::new()));
    let _ = &opts.source_map;
    drop(TypeDefWithObjectOpts::default().with_source_map(Id::from("generated-id").into()));
    drop(value.with_optional(generated_value::<bool>()));
    drop(value.with_scalar(String::new()));
    let opts = TypeDefWithScalarOpts::default();
    drop(value.with_scalar_opts(String::new(), &opts));
    let _ = &opts.description;
    drop(TypeDefWithScalarOpts::default().with_description(String::new()));
}
#[allow(deprecated)]
fn reach_up(value: &Up) {
    drop(value.description());
    drop(value.id());
    drop(value.name());
    drop(value.original_module());
    drop(value.path());
    drop(value.run());
}
#[allow(deprecated)]
fn reach_upgroup(value: &UpGroup) {
    drop(value.id());
    drop(value.list());
    drop(value.run());
}
#[allow(deprecated)]
fn reach_volume(value: &Volume) {
    drop(value.id());
}
#[allow(deprecated)]
fn reach_workspace(value: &Workspace) {
    drop(value.address());
    drop(value.changes());
    drop(value.checks());
    let opts = WorkspaceChecksOpts::default();
    drop(value.checks_opts(&opts));
    let _ = &opts.include;
    drop(WorkspaceChecksOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_generate;
    drop(WorkspaceChecksOpts::default().with_no_generate(generated_value::<bool>()));
    let _ = &opts.only_generate;
    drop(WorkspaceChecksOpts::default().with_only_generate(generated_value::<bool>()));
    let _ = &opts.skip;
    drop(WorkspaceChecksOpts::default().with_skip(generated_value::<Vec<String>>()));
    drop(value.config_file());
    drop(value.config_read());
    let opts = WorkspaceConfigReadOpts::default();
    drop(value.config_read_opts(&opts));
    let _ = &opts.key;
    drop(WorkspaceConfigReadOpts::default().with_key(String::new()));
    drop(value.cwd());
    drop(value.directory(String::new()));
    let opts = WorkspaceDirectoryOpts::default();
    drop(value.directory_opts(String::new(), &opts));
    let _ = &opts.exclude;
    drop(WorkspaceDirectoryOpts::default().with_exclude(generated_value::<Vec<String>>()));
    let _ = &opts.gitignore;
    drop(WorkspaceDirectoryOpts::default().with_gitignore(generated_value::<bool>()));
    let _ = &opts.include;
    drop(WorkspaceDirectoryOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.env_list());
    drop(value.export());
    drop(value.file(String::new()));
    drop(value.find_up(String::new()));
    let opts = WorkspaceFindUpOpts::default();
    drop(value.find_up_opts(String::new(), &opts));
    let _ = &opts.from;
    drop(WorkspaceFindUpOpts::default().with_from(String::new()));
    drop(value.generators());
    let opts = WorkspaceGeneratorsOpts::default();
    drop(value.generators_opts(&opts));
    let _ = &opts.include;
    drop(WorkspaceGeneratorsOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.git());
    drop(value.glob(String::new()));
    drop(value.id());
    drop(value.migrate());
    drop(value.module(String::new()));
    drop(value.module_source(String::new()));
    drop(value.modules());
    drop(value.sdk(String::new()));
    drop(value.sdks());
    drop(value.search(String::new()));
    let opts = WorkspaceSearchOpts::default();
    drop(value.search_opts(String::new(), &opts));
    let _ = &opts.dotall;
    drop(WorkspaceSearchOpts::default().with_dotall(generated_value::<bool>()));
    let _ = &opts.files_only;
    drop(WorkspaceSearchOpts::default().with_files_only(generated_value::<bool>()));
    let _ = &opts.globs;
    drop(WorkspaceSearchOpts::default().with_globs(generated_value::<Vec<String>>()));
    let _ = &opts.insensitive;
    drop(WorkspaceSearchOpts::default().with_insensitive(generated_value::<bool>()));
    let _ = &opts.limit;
    drop(WorkspaceSearchOpts::default().with_limit(generated_value::<i64>()));
    let _ = &opts.literal;
    drop(WorkspaceSearchOpts::default().with_literal(generated_value::<bool>()));
    let _ = &opts.multiline;
    drop(WorkspaceSearchOpts::default().with_multiline(generated_value::<bool>()));
    let _ = &opts.paths;
    drop(WorkspaceSearchOpts::default().with_paths(generated_value::<Vec<String>>()));
    let _ = &opts.skip_hidden;
    drop(WorkspaceSearchOpts::default().with_skip_hidden(generated_value::<bool>()));
    let _ = &opts.skip_ignored;
    drop(WorkspaceSearchOpts::default().with_skip_ignored(generated_value::<bool>()));
    drop(value.services());
    let opts = WorkspaceServicesOpts::default();
    drop(value.services_opts(&opts));
    let _ = &opts.include;
    drop(WorkspaceServicesOpts::default().with_include(generated_value::<Vec<String>>()));
    drop(value.with_changes(Id::from("generated-id")));
    drop(value.with_config_env(String::new()));
    let opts = WorkspaceWithConfigEnvOpts::default();
    drop(value.with_config_env_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithConfigEnvOpts::default().with_here(generated_value::<bool>()));
    drop(value.with_config_value(String::new(), String::new()));
    let opts = WorkspaceWithConfigValueOpts::default();
    drop(value.with_config_value_opts(String::new(), String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithConfigValueOpts::default().with_here(generated_value::<bool>()));
    let _ = &opts.values;
    drop(WorkspaceWithConfigValueOpts::default().with_values(generated_value::<Vec<String>>()));
    drop(value.with_init_client(String::new(), String::new(), String::new()));
    let opts = WorkspaceWithInitClientOpts::default();
    drop(value.with_init_client_opts(String::new(), String::new(), String::new(), &opts));
    let _ = &opts.args;
    drop(WorkspaceWithInitClientOpts::default().with_args(generated_value::<dagger_sdk::Json>()));
    let _ = &opts.here;
    drop(WorkspaceWithInitClientOpts::default().with_here(generated_value::<bool>()));
    let _ = &opts.no_generate;
    drop(WorkspaceWithInitClientOpts::default().with_no_generate(generated_value::<bool>()));
    drop(value.with_init_module(String::new(), String::new()));
    let opts = WorkspaceWithInitModuleOpts::default();
    drop(value.with_init_module_opts(String::new(), String::new(), &opts));
    let _ = &opts.args;
    drop(WorkspaceWithInitModuleOpts::default().with_args(generated_value::<dagger_sdk::Json>()));
    let _ = &opts.here;
    drop(WorkspaceWithInitModuleOpts::default().with_here(generated_value::<bool>()));
    let _ = &opts.include;
    drop(WorkspaceWithInitModuleOpts::default().with_include(generated_value::<Vec<String>>()));
    let _ = &opts.no_generate;
    drop(WorkspaceWithInitModuleOpts::default().with_no_generate(generated_value::<bool>()));
    let _ = &opts.path;
    drop(WorkspaceWithInitModuleOpts::default().with_path(String::new()));
    let _ = &opts.source;
    drop(WorkspaceWithInitModuleOpts::default().with_source(String::new()));
    drop(value.with_module(String::new()));
    let opts = WorkspaceWithModuleOpts::default();
    drop(value.with_module_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithModuleOpts::default().with_here(generated_value::<bool>()));
    let _ = &opts.name;
    drop(WorkspaceWithModuleOpts::default().with_name(String::new()));
    drop(value.with_new_directory(String::new(), Id::from("generated-id")));
    drop(value.with_new_file(String::new(), String::new()));
    let opts = WorkspaceWithNewFileOpts::default();
    drop(value.with_new_file_opts(String::new(), String::new(), &opts));
    let _ = &opts.permissions;
    drop(WorkspaceWithNewFileOpts::default().with_permissions(generated_value::<i64>()));
    drop(value.with_sdk(String::new()));
    let opts = WorkspaceWithSdkOpts::default();
    drop(value.with_sdk_opts(String::new(), &opts));
    let _ = &opts.as_sdk_name;
    drop(WorkspaceWithSdkOpts::default().with_as_sdk_name(String::new()));
    let _ = &opts.here;
    drop(WorkspaceWithSdkOpts::default().with_here(generated_value::<bool>()));
    let _ = &opts.name;
    drop(WorkspaceWithSdkOpts::default().with_name(String::new()));
    drop(value.with_updated_lock());
    drop(value.with_workdir(String::new()));
    drop(value.without_config_env(String::new()));
    let opts = WorkspaceWithoutConfigEnvOpts::default();
    drop(value.without_config_env_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithoutConfigEnvOpts::default().with_here(generated_value::<bool>()));
    drop(value.without_config_value(String::new()));
    let opts = WorkspaceWithoutConfigValueOpts::default();
    drop(value.without_config_value_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithoutConfigValueOpts::default().with_here(generated_value::<bool>()));
    drop(value.without_directory(String::new()));
    drop(value.without_file(String::new()));
    drop(value.without_module(String::new()));
    let opts = WorkspaceWithoutModuleOpts::default();
    drop(value.without_module_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithoutModuleOpts::default().with_here(generated_value::<bool>()));
    drop(value.without_sdk(String::new()));
    let opts = WorkspaceWithoutSdkOpts::default();
    drop(value.without_sdk_opts(String::new(), &opts));
    let _ = &opts.here;
    drop(WorkspaceWithoutSdkOpts::default().with_here(generated_value::<bool>()));
}
#[allow(deprecated)]
fn reach_workspacegit(value: &WorkspaceGit) {
    drop(value.head());
    drop(value.id());
    drop(value.uncommitted());
}
#[allow(deprecated)]
fn reach_workspacemigration(value: &WorkspaceMigration) {
    drop(value.changes());
    drop(value.id());
    drop(value.steps());
}
#[allow(deprecated)]
fn reach_workspacemigrationstep(value: &WorkspaceMigrationStep) {
    drop(value.changes());
    drop(value.code());
    drop(value.description());
    drop(value.id());
    drop(value.warnings());
}
#[allow(deprecated)]
fn reach_workspacemodule(value: &WorkspaceModule) {
    drop(value.entrypoint());
    drop(value.id());
    drop(value.name());
    drop(value.settings());
    drop(value.source());
}
#[allow(deprecated)]
fn reach_workspacemodulesetting(value: &WorkspaceModuleSetting) {
    drop(value.description());
    drop(value.id());
    drop(value.is_list());
    drop(value.key());
    drop(value.value());
}
#[allow(deprecated)]
fn reach_workspacesdk(value: &WorkspaceSdk) {
    drop(value.clients());
    drop(value.id());
    drop(value.modules());
    drop(value.name());
    drop(value.r#ref());
}
#[test]
fn generated_public_reachability() {
    let _ = core::mem::size_of::<Address>();
    let _ = reach_address as fn(&Address);
    let _ = core::mem::size_of::<BuildArg>();
    let _ = reach_buildarg_input as fn(&BuildArg);
    let _ = core::mem::size_of::<CacheSharingMode>();
    #[allow(deprecated)]
    let _ = CacheSharingMode::Locked;
    #[allow(deprecated)]
    let _ = CacheSharingMode::Private;
    #[allow(deprecated)]
    let _ = CacheSharingMode::Shared;
    let _ = core::mem::size_of::<CacheVolume>();
    let _ = reach_cachevolume as fn(&CacheVolume);
    let _ = core::mem::size_of::<Changeset>();
    let _ = reach_changeset as fn(&Changeset);
    let _ = core::mem::size_of::<ChangesetMergeConflict>();
    #[allow(deprecated)]
    let _ = ChangesetMergeConflict::Fail;
    #[allow(deprecated)]
    let _ = ChangesetMergeConflict::FailEarly;
    #[allow(deprecated)]
    let _ = ChangesetMergeConflict::LeaveConflictMarkers;
    #[allow(deprecated)]
    let _ = ChangesetMergeConflict::PreferOurs;
    #[allow(deprecated)]
    let _ = ChangesetMergeConflict::PreferTheirs;
    let _ = core::mem::size_of::<ChangesetsMergeConflict>();
    #[allow(deprecated)]
    let _ = ChangesetsMergeConflict::Fail;
    #[allow(deprecated)]
    let _ = ChangesetsMergeConflict::FailEarly;
    let _ = core::mem::size_of::<Check>();
    let _ = reach_check as fn(&Check);
    let _ = core::mem::size_of::<CheckGroup>();
    let _ = reach_checkgroup as fn(&CheckGroup);
    let _ = core::mem::size_of::<ClientFilesyncMirror>();
    let _ = reach_clientfilesyncmirror as fn(&ClientFilesyncMirror);
    let _ = core::mem::size_of::<Cloud>();
    let _ = reach_cloud as fn(&Cloud);
    let _ = core::mem::size_of::<Container>();
    let _ = reach_container as fn(&Container);
    let _ = core::mem::size_of::<CurrentModule>();
    let _ = reach_currentmodule as fn(&CurrentModule);
    let _ = core::mem::size_of::<CurrentModuleAsSdk>();
    let _ = reach_currentmoduleassdk as fn(&CurrentModuleAsSdk);
    let _ = core::mem::size_of::<CurrentModuleAsSdkClient>();
    let _ = reach_currentmoduleassdkclient as fn(&CurrentModuleAsSdkClient);
    let _ = core::mem::size_of::<CurrentModuleAsSdkModule>();
    let _ = reach_currentmoduleassdkmodule as fn(&CurrentModuleAsSdkModule);
    let _ = core::mem::size_of::<DiffStat>();
    let _ = reach_diffstat as fn(&DiffStat);
    let _ = core::mem::size_of::<DiffStatKind>();
    #[allow(deprecated)]
    let _ = DiffStatKind::Added;
    #[allow(deprecated)]
    let _ = DiffStatKind::Modified;
    #[allow(deprecated)]
    let _ = DiffStatKind::Removed;
    #[allow(deprecated)]
    let _ = DiffStatKind::Renamed;
    let _ = core::mem::size_of::<Directory>();
    let _ = reach_directory as fn(&Directory);
    let _ = core::mem::size_of::<Engine>();
    let _ = reach_engine as fn(&Engine);
    let _ = core::mem::size_of::<EngineCache>();
    let _ = reach_enginecache as fn(&EngineCache);
    let _ = core::mem::size_of::<EngineCacheEntry>();
    let _ = reach_enginecacheentry as fn(&EngineCacheEntry);
    let _ = core::mem::size_of::<EngineCacheEntrySet>();
    let _ = reach_enginecacheentryset as fn(&EngineCacheEntrySet);
    let _ = core::mem::size_of::<EnumTypeDef>();
    let _ = reach_enumtypedef as fn(&EnumTypeDef);
    let _ = core::mem::size_of::<EnumValueTypeDef>();
    let _ = reach_enumvaluetypedef as fn(&EnumValueTypeDef);
    let _ = core::mem::size_of::<EnvFile>();
    let _ = reach_envfile as fn(&EnvFile);
    let _ = core::mem::size_of::<EnvVariable>();
    let _ = reach_envvariable as fn(&EnvVariable);
    let _ = core::mem::size_of::<Error>();
    let _ = reach_error as fn(&Error);
    let _ = core::mem::size_of::<ErrorValue>();
    let _ = reach_errorvalue as fn(&ErrorValue);
    let _ = core::mem::size_of::<ExistsType>();
    #[allow(deprecated)]
    let _ = ExistsType::DirectoryType;
    #[allow(deprecated)]
    let _ = ExistsType::RegularType;
    #[allow(deprecated)]
    let _ = ExistsType::SymlinkType;
    let _ = core::mem::size_of::<ExportableClient>();
    let _ = reach_exportableclient as fn(&ExportableClient);
    let _ = reach_exportable_trait::<ExportableClient> as fn(&ExportableClient);
    let _ = core::mem::size_of::<FieldTypeDef>();
    let _ = reach_fieldtypedef as fn(&FieldTypeDef);
    let _ = core::mem::size_of::<File>();
    let _ = reach_file as fn(&File);
    let _ = core::mem::size_of::<FileType>();
    #[allow(deprecated)]
    let _ = FileType::Directory;
    #[allow(deprecated)]
    let _ = FileType::Regular;
    #[allow(deprecated)]
    let _ = FileType::Symlink;
    #[allow(deprecated)]
    let _ = FileType::Unknown;
    let _ = core::mem::size_of::<Function>();
    let _ = reach_function as fn(&Function);
    let _ = core::mem::size_of::<FunctionArg>();
    let _ = reach_functionarg as fn(&FunctionArg);
    let _ = core::mem::size_of::<FunctionCachePolicy>();
    #[allow(deprecated)]
    let _ = FunctionCachePolicy::Default;
    #[allow(deprecated)]
    let _ = FunctionCachePolicy::Never;
    #[allow(deprecated)]
    let _ = FunctionCachePolicy::PerSession;
    let _ = core::mem::size_of::<FunctionCall>();
    let _ = reach_functioncall as fn(&FunctionCall);
    let _ = core::mem::size_of::<FunctionCallArgValue>();
    let _ = reach_functioncallargvalue as fn(&FunctionCallArgValue);
    let _ = core::mem::size_of::<GeneratedCode>();
    let _ = reach_generatedcode as fn(&GeneratedCode);
    let _ = core::mem::size_of::<Generator>();
    let _ = reach_generator as fn(&Generator);
    let _ = core::mem::size_of::<GeneratorGroup>();
    let _ = reach_generatorgroup as fn(&GeneratorGroup);
    let _ = core::mem::size_of::<GitRef>();
    let _ = reach_gitref as fn(&GitRef);
    let _ = core::mem::size_of::<GitRepository>();
    let _ = reach_gitrepository as fn(&GitRepository);
    let _ = core::mem::size_of::<HttpState>();
    let _ = reach_httpstate as fn(&HttpState);
    let _ = core::mem::size_of::<HealthcheckConfig>();
    let _ = reach_healthcheckconfig as fn(&HealthcheckConfig);
    let _ = core::mem::size_of::<Host>();
    let _ = reach_host as fn(&Host);
    let _ = core::mem::size_of::<Id>();
    let _ = core::mem::size_of::<ImageLayerCompression>();
    #[allow(deprecated)]
    let _ = ImageLayerCompression::EStarGz;
    #[allow(deprecated)]
    let _ = ImageLayerCompression::Gzip;
    #[allow(deprecated)]
    let _ = ImageLayerCompression::Uncompressed;
    #[allow(deprecated)]
    let _ = ImageLayerCompression::Zstd;
    let _ = core::mem::size_of::<ImageMediaTypes>();
    #[allow(deprecated)]
    let _ = ImageMediaTypes::DockerMediaTypes;
    #[allow(deprecated)]
    let _ = ImageMediaTypes::OciMediaTypes;
    let _ = core::mem::size_of::<InputTypeDef>();
    let _ = reach_inputtypedef as fn(&InputTypeDef);
    let _ = core::mem::size_of::<InterfaceTypeDef>();
    let _ = reach_interfacetypedef as fn(&InterfaceTypeDef);
    let _ = core::mem::size_of::<Json>();
    let _ = core::mem::size_of::<JsonValue>();
    let _ = reach_jsonvalue as fn(&JsonValue);
    let _ = core::mem::size_of::<Llm>();
    let _ = reach_llm as fn(&Llm);
    let _ = core::mem::size_of::<LlmContentBlock>();
    let _ = reach_llmcontentblock as fn(&LlmContentBlock);
    let _ = core::mem::size_of::<LlmContentBlockInput>();
    let _ = reach_llmcontentblockinput_input as fn(&LlmContentBlockInput);
    let _ = core::mem::size_of::<LlmContentBlockKind>();
    #[allow(deprecated)]
    let _ = LlmContentBlockKind::Text;
    #[allow(deprecated)]
    let _ = LlmContentBlockKind::Thinking;
    #[allow(deprecated)]
    let _ = LlmContentBlockKind::ToolCall;
    #[allow(deprecated)]
    let _ = LlmContentBlockKind::ToolResult;
    let _ = core::mem::size_of::<LlmMessage>();
    let _ = reach_llmmessage as fn(&LlmMessage);
    let _ = core::mem::size_of::<LlmMessageRole>();
    #[allow(deprecated)]
    let _ = LlmMessageRole::Assistant;
    #[allow(deprecated)]
    let _ = LlmMessageRole::System;
    #[allow(deprecated)]
    let _ = LlmMessageRole::User;
    let _ = core::mem::size_of::<LlmTokenUsage>();
    let _ = reach_llmtokenusage as fn(&LlmTokenUsage);
    let _ = core::mem::size_of::<Label>();
    let _ = reach_label as fn(&Label);
    let _ = core::mem::size_of::<ListTypeDef>();
    let _ = reach_listtypedef as fn(&ListTypeDef);
    let _ = core::mem::size_of::<Module>();
    let _ = reach_module as fn(&Module);
    let _ = core::mem::size_of::<ModuleConfigClient>();
    let _ = reach_moduleconfigclient as fn(&ModuleConfigClient);
    let _ = core::mem::size_of::<ModuleSource>();
    let _ = reach_modulesource as fn(&ModuleSource);
    let _ = core::mem::size_of::<ModuleSourceExperimentalFeature>();
    #[allow(deprecated)]
    let _ = ModuleSourceExperimentalFeature::SelfCalls;
    let _ = core::mem::size_of::<ModuleSourceKind>();
    #[allow(deprecated)]
    let _ = ModuleSourceKind::DirSource;
    #[allow(deprecated)]
    let _ = ModuleSourceKind::GitSource;
    #[allow(deprecated)]
    let _ = ModuleSourceKind::LocalSource;
    let _ = core::mem::size_of::<NetworkProtocol>();
    #[allow(deprecated)]
    let _ = NetworkProtocol::Tcp;
    #[allow(deprecated)]
    let _ = NetworkProtocol::Udp;
    let _ = core::mem::size_of::<NodeClient>();
    let _ = reach_nodeclient as fn(&NodeClient);
    let _ = reach_node_trait::<NodeClient> as fn(&NodeClient);
    let _ = core::mem::size_of::<ObjectTypeDef>();
    let _ = reach_objecttypedef as fn(&ObjectTypeDef);
    let _ = core::mem::size_of::<PatchConflict>();
    #[allow(deprecated)]
    let _ = PatchConflict::Fail;
    #[allow(deprecated)]
    let _ = PatchConflict::LeaveConflictMarkers;
    let _ = core::mem::size_of::<PipelineLabel>();
    let _ = reach_pipelinelabel_input as fn(&PipelineLabel);
    let _ = core::mem::size_of::<Platform>();
    let _ = core::mem::size_of::<Port>();
    let _ = reach_port as fn(&Port);
    let _ = core::mem::size_of::<PortForward>();
    let _ = reach_portforward_input as fn(&PortForward);
    let _ = core::mem::size_of::<Query>();
    let _ = reach_query as fn(&Query);
    let _ = core::mem::size_of::<RegistryProtocol>();
    #[allow(deprecated)]
    let _ = RegistryProtocol::Http;
    #[allow(deprecated)]
    let _ = RegistryProtocol::Https;
    let _ = core::mem::size_of::<RemoteGitMirror>();
    let _ = reach_remotegitmirror as fn(&RemoteGitMirror);
    let _ = core::mem::size_of::<ReturnType>();
    #[allow(deprecated)]
    let _ = ReturnType::Any;
    #[allow(deprecated)]
    let _ = ReturnType::Failure;
    #[allow(deprecated)]
    let _ = ReturnType::Success;
    let _ = core::mem::size_of::<SdkConfig>();
    let _ = reach_sdkconfig as fn(&SdkConfig);
    let _ = core::mem::size_of::<ScalarTypeDef>();
    let _ = reach_scalartypedef as fn(&ScalarTypeDef);
    let _ = core::mem::size_of::<Schema>();
    let _ = reach_schema as fn(&Schema);
    let _ = core::mem::size_of::<SearchResult>();
    let _ = reach_searchresult as fn(&SearchResult);
    let _ = core::mem::size_of::<SearchSubmatch>();
    let _ = reach_searchsubmatch as fn(&SearchSubmatch);
    let _ = core::mem::size_of::<Secret>();
    let _ = reach_secret as fn(&Secret);
    let _ = core::mem::size_of::<Service>();
    let _ = reach_service as fn(&Service);
    let _ = core::mem::size_of::<Socket>();
    let _ = reach_socket as fn(&Socket);
    let _ = core::mem::size_of::<SourceMap>();
    let _ = reach_sourcemap as fn(&SourceMap);
    let _ = core::mem::size_of::<Stat>();
    let _ = reach_stat as fn(&Stat);
    let _ = core::mem::size_of::<SyncerClient>();
    let _ = reach_syncerclient as fn(&SyncerClient);
    let _ = reach_syncer_trait::<SyncerClient> as fn(&SyncerClient);
    let _ = core::mem::size_of::<Terminal>();
    let _ = reach_terminal as fn(&Terminal);
    let _ = core::mem::size_of::<TypeDef>();
    let _ = reach_typedef as fn(&TypeDef);
    let _ = core::mem::size_of::<TypeDefKind>();
    #[allow(deprecated)]
    let _ = TypeDefKind::BooleanKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::EnumKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::FloatKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::InputKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::IntegerKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::InterfaceKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::ListKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::ObjectKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::ScalarKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::StringKind;
    #[allow(deprecated)]
    let _ = TypeDefKind::VoidKind;
    let _ = core::mem::size_of::<Up>();
    let _ = reach_up as fn(&Up);
    let _ = core::mem::size_of::<UpGroup>();
    let _ = reach_upgroup as fn(&UpGroup);
    let _ = core::mem::size_of::<Volume>();
    let _ = reach_volume as fn(&Volume);
    let _ = core::mem::size_of::<Workspace>();
    let _ = reach_workspace as fn(&Workspace);
    let _ = core::mem::size_of::<WorkspaceGit>();
    let _ = reach_workspacegit as fn(&WorkspaceGit);
    let _ = core::mem::size_of::<WorkspaceMigration>();
    let _ = reach_workspacemigration as fn(&WorkspaceMigration);
    let _ = core::mem::size_of::<WorkspaceMigrationStep>();
    let _ = reach_workspacemigrationstep as fn(&WorkspaceMigrationStep);
    let _ = core::mem::size_of::<WorkspaceModule>();
    let _ = reach_workspacemodule as fn(&WorkspaceModule);
    let _ = core::mem::size_of::<WorkspaceModuleSetting>();
    let _ = reach_workspacemodulesetting as fn(&WorkspaceModuleSetting);
    let _ = core::mem::size_of::<WorkspaceSdk>();
    let _ = reach_workspacesdk as fn(&WorkspaceSdk);
}
