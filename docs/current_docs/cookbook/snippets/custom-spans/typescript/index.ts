import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async foo(): Promise<Directory> {
    // define the files to be created and their contents
    const files = {
      "file1.txt": "foo",
      "file2.txt": "bar",
      "file3.txt": "baz",
    }

    // set up an alpine container with the directory mounted
    let container = dag.container()
      .from("alpine:latest")
      .withDirectory("/results", dag.directory())
      .withWorkdir("/results")

    for (const [name, content] of Object.entries(files)) {
      // create files
      container = container.withNewFile(name, content)
      // emit custom spans for each file created
      const log = `Created file: ${name} with contents: ${content}`
      console.log(log)
    }

    return container.directory("/results")
  }
}
