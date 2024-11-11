import dagger
from dagger import dag, field, function, object_type


@object_type
class ExtPythonSdk:
    required_paths: list[str] = field(default=list)

    @function
    async def codegen(
        self,
        mod_source: dagger.ModuleSource,
        introspection_json: dagger.File,
    ) -> dagger.GeneratedCode:
        sdk = self.common(mod_source, introspection_json).with_updates()
        return (
            dag.generated_code(sdk.container().directory(await sdk.context_dir_path()))
            .with_vcs_generated_paths(["sdk/**"])
            .with_vcs_ignored_paths(["sdk"])
        )

    @function
    def module_runtime(
        self,
        mod_source: dagger.ModuleSource,
        introspection_json: dagger.File,
    ) -> dagger.Container:
        return self.common(mod_source, introspection_json).with_install().container()

    def common(
        self,
        mod_source: dagger.ModuleSource,
        introspection_json: dagger.File,
    ) -> dagger.PythonSdk:
        base = (
            dag.python_sdk(
                # Not really necessary with context directory, but simplifies
                # the test setup.
                sdk_source_dir=dag.current_module().source().directory("sdk"),
            )
            .without_user_config()
            .load(mod_source)
        )
        ctr = (
            base.with_base()
            .container()
            .with_exec(["apt-get", "update"])
            .with_exec(["apt-get", "install", "--no-install-recommends", "-y", "git"])
        )
        return (
            base.with_container(ctr)
            .with_sdk(introspection_json)
            .with_template()
            .with_source()
        )
