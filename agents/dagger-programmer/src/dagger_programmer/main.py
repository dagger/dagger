from typing import Annotated
from dagger import dag, Doc, Directory, function, object_type


@object_type
class DaggerProgrammer:
    @function
    async def translate(
        self,
        repo: Annotated[str, Doc("The git repo of the module to translate")],
        language: Annotated[str, Doc("The language to translate the module to")],
        subpath: Annotated[str, Doc("The path to the module in the git repository")] = ""
    ) -> Directory:
        """Returns a dagger module in the specified language translated from the provided module"""
        source_dir = dag.git(repo).head().tree()
        if subpath != "":
            source_dir = source_dir.directory(subpath)

        source_mod_sdk = await source_dir.as_module().sdk().source()
        source_mod_name = await source_dir.as_module().name()

        # Create a mod/workspace for the translated sdk
        ws = dag.module_workspace(language, source_mod_name)
        main_file_paths = {
            "go": "main.go",
            "python": f"src/{source_mod_name}/main.py",
            "typescript": "src/index.ts",
            "php": "src/MyModule.php",
            "java": "src/main/java/io/dagger/modules/mymodule/MyModule.java"
        }
        source_mod_file = await source_dir.file(main_file_paths[source_mod_sdk]).contents()

        # translate the source mod to the target sdk
        work = (
            dag
            .llm()
            .with_module_workspace(ws)
            .with_prompt(f"""
You are an expert translating Dagger code between {source_mod_sdk} and {language} SDKs.
Use the 'getReference' tool to learn how to use each Dagger SDK.
You have access to a workspace with the ability to read, write, and test code.
Before writing code, always use `getReference` for both SDKs you are working with.
Use the reference snippets to help you translate the assignment below.
Always write the translated code to the workspace you've been provided.
Always run test after writing the code to ensure it passes.
If test fails, read the error message and fix the code until it passes.
Do not stop until the you have translated the provided code and test passes.

Your assignment is to translate the following code from {source_mod_sdk} to {language} and write the code to your workspace:
<assignment>
{source_mod_file}
</assignment>

ALWAYS WRITE YOUR GENERATED CODE TO THE WORKSPACE PROVIDED. TEST MUST PASS.
            """)
            .module_workspace()
        )
        # Check again that test passes because LLMs lie
        await work.test()
        # return work output
        return work.workspace()
