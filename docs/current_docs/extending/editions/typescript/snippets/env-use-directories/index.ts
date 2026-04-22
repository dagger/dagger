import { dag, object, func, Directory } from "@dagger.io/dagger"

@object()
export class MyModule {
  @func()
  agent(): Directory {
    const dir = dag.git("github.com/dagger/dagger").branch("main").tree()
    const environment = dag
      .env()
      .withDirectoryInput("source", dir, "the source directory to use")
      .withDirectoryOutput("result", "the updated directory")

    const work = dag
      .llm()
      .withEnv(environment)
      .withPrompt(
        `You have access to a directory containing various files.
        Translate only the README file in the directory to French and Spanish.
        Ensure that the translations are accurate and maintain the original formatting.
        Do not modify any other files in the directory.
        Create a sub-directory named 'translations' to store the translated files.
        For French, add an 'fr' suffix to the translated file name.
        For Spanish, add an 'es' suffix to the translated file name.
        Do not create any other new files or directories.
        Do not delete any files or directories.
        Do not investigate any sub-directories.
        Once complete, return the 'translations' directory.
        `,
      )

    return work.env().output("result").asDirectory()
  }
}
