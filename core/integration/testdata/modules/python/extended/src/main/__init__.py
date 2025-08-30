import dagger
from dagger import dag


@dagger.object_type
class ExtPythonSdk:
    @dagger.function
    async def codegen(
        self,
        mod_source: dagger.ModuleSource,
        introspection_json: dagger.File,
    ) -> dagger.GeneratedCode:
        sdk = self.common(mod_source, introspection_json)
        gitignore = await dag.current_module().source().file(".gitignore").contents()
        gitattrs = await dag.current_module().source().file(".gitattributes").contents()
        return (
            dag.generated_code(sdk.container().directory(await sdk.context_dir_path()))
            .with_vcs_generated_paths(gitattrs.split("\n"))
            .with_vcs_ignored_paths(gitignore.split("\n"))
        )

    @dagger.function
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
                # Note that the ``+defaultPath=".."`` defined in the original
                # module's constructor is getting resolved relative to this
                # one, so it ends up trying to load this module's parent
                # directory (`core/integration/testdata/modules/python`)`.
                # In a production use case this could come from git for example.
                sdk_source_dir=dag.current_module().source().directory("sdk"),
            )
            # without user config discovery here for testing purposes
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
            .with_updates()
        )
