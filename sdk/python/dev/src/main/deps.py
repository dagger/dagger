import dagger
from dagger.mod import Annotated, Doc, field, function, object_type

from .consts import DEP_ENVS, PYTHON_VERSION
from .utils import cache, from_host_file, python_base


@object_type
class Hatch:
    """Hatch is a modern, extensible dependency manager for Python projects."""

    ctr: Annotated[
        dagger.Container,
        Doc("Container to run hatch in"),
    ] = field(default=lambda: python_base())

    version: Annotated[
        str,
        Doc("hatch version"),
    ] = field(default="1.7.0")

    cfg: Annotated[
        dagger.File | None,
        Doc("hatch.toml file"),
    ] = field(default=None)

    cache_dir: Annotated[
        str,
        Doc("Cache directory path"),
    ] = field(default="/root/.cache/hatch")

    @function
    def base(self) -> dagger.Container:
        """Base container with hatch installed."""
        ctr = self.ctr
        if self.cache_dir:
            ctr = ctr.with_(
                cache(
                    self.cache_dir,
                    keys=["hatch", f"py{PYTHON_VERSION}", "slim"],
                )
            )
        ctr = ctr.with_exec(["pip", "install", f"hatch=={self.version}"]).with_workdir(
            "/work"
        )
        if self.cfg:
            ctr = ctr.with_mounted_file("hatch.toml", self.cfg)
        return ctr

    @function
    def requirements(
        self,
        env: Annotated[
            str,
            Doc("Show requirements for this environment"),
        ],
    ) -> dagger.File:
        """Enumerate an environment's dependencies as a list of requirements."""
        file = f"requirements.{env}.in"
        return (
            self.base()
            .pipeline(f"{env} requirements")
            .with_env_variable("HATCH_ENV", env)
            .with_exec(
                ["hatch", "dep", "show", "requirements", "-e"],
                redirect_stdout=file,
            )
            .file(file)
        )

    @function
    def build(
        self,
        src: Annotated[
            dagger.Directory,
            Doc("Directory with the source code to build"),
        ],
        version: Annotated[
            str,
            Doc("The version to build"),
        ],
    ) -> dagger.Directory:
        """Build sdist and wheel artifacts for the library."""
        return (
            self.base()
            .with_mounted_directory("/work", src)
            .with_env_variable("SETUPTOOLS_SCM_PRETEND_VERSION", version)
            .with_focus()
            .with_exec(["hatch", "build", "--clean"])
            .directory("dist")
        )

    @function
    def publish(
        self,
        artifacts: Annotated[
            dagger.Directory,
            Doc("Directory with the artifacts to publish"),
        ],
        token: Annotated[
            dagger.Secret,
            Doc("PyPI token"),
        ],
        repo: Annotated[
            str,
            Doc("PyPI repository"),
        ] = "main",
    ) -> dagger.Container:
        """Publish the SDK to PyPI."""
        return (
            self.base()
            .with_env_variable("HATCH_INDEX_REPO", repo)
            .with_env_variable("HATCH_INDEX_USER", "__token__")
            .with_secret_variable("HATCH_INDEX_AUTH", token)
            .with_mounted_directory("/dist", artifacts)
            .with_focus()
            .with_exec(["hatch", "publish", "/dist"])
        )


@object_type
class PipTools:
    """A set of command line tools to pin dependencies in a requirements.txt file."""

    ctr: Annotated[
        dagger.Container,
        Doc("Container to run pip-tools in"),
    ] = field(default=lambda: python_base())

    version: Annotated[
        str,
        Doc("pip-tools version"),
    ] = field(default="7.3.0")

    cache_dir: Annotated[
        str,
        Doc("Cache directory path"),
    ] = field(default="/root/.cache/pip-tools")

    @function
    def base(self) -> dagger.Container:
        """Base container with pip-tools installed."""
        ctr = self.ctr
        if self.cache_dir:
            ctr = ctr.with_(
                cache(
                    self.cache_dir,
                    keys=["pip-tools", f"py{PYTHON_VERSION}", "slim"],
                )
            )
        return ctr.with_exec(
            ["pip", "install", f"pip-tools=={self.version}"]
        ).with_workdir("/work")

    @function(name="compile")
    def compile_(
        self,
        requirements: Annotated[
            dagger.File,
            Doc("The input requirements file to compile"),
        ],
        output: Annotated[
            str,
            Doc("The output file name"),
        ] = "requirements.txt",
        command: Annotated[
            str | None,
            Doc("Command to annotate in the header"),
        ] = None,
    ) -> dagger.File:
        """Compile a requirements.in file into a requirements.txt file."""
        ctr = self.base()
        if command:
            ctr = ctr.with_env_variable("CUSTOM_COMPILE_COMMAND", command)
        return (
            ctr.with_mounted_file(
                "requirements.in",
                requirements,
            )
            .with_exec(
                [
                    "pip-compile",
                    "--annotate",
                    "--upgrade",
                    "--resolver=backtracking",
                    f"--output-file={output}",
                    "requirements.in",
                ],
            )
            .file(output)
        )


@object_type
class Deps:
    """Manage the SDK's development dependencies for a hatch environment."""

    env: Annotated[
        str,
        Doc(f"The hatch environment to use. Can be one of {DEP_ENVS}"),
    ] = field()

    hatch_config: Annotated[
        dagger.File,
        Doc("The hatch.toml file with the environments and their dependencies"),
    ] = field(default=lambda: from_host_file("hatch.toml"))

    @function
    def hatch(self) -> Hatch:
        """Run hatch tasks."""
        return Hatch(cfg=self.hatch_config)

    @function
    def requirements(self) -> dagger.File:
        """Return the constrained development dependencies."""
        return self.hatch().requirements(self.env)

    @function
    def lock(self) -> dagger.File:
        """Update the pinned development dependencies."""
        return PipTools().compile_(
            requirements=self.requirements(),
            output=f"{self.env}.txt",
            command=f"dagger dl -m dev deps --env={self.env} lock -o requirements",
        )
