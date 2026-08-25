import dagger
from dagger import dag, function, object_type


@object_type
class MyModule:
    @function
    def agent(self) -> dagger.Directory:
        dirname = dag.git("github.com/dagger/dagger").branch("main").tree()
        environment = (
            dag.env()
            .with_directory_input("source", dirname, "the source directory to use")
            .with_directory_output("result", "the updated directory")
        )

        work = (
            dag.llm()
            .with_env(environment)
            .with_prompt(
                """
                You have access to a directory containing various files.
                Translate only the README file in the directory to French and Spanish.
                Ensure that the translations are accurate and maintain the original
                formatting.
                Do not modify any other files in the directory.
                Create a subdirectory named translations to store the translated files.
                For French, add an 'fr' suffix to the translated file name.
                For Spanish, add an 'es' suffix to the translated file name.
                Do not create any other new files or directories.
                Do not delete any files or directories.
                Do not investigate any sub-directories.
                Once complete, return the 'translations' directory.
                """
            )
        )

        return work.env().output("result").as_directory()
