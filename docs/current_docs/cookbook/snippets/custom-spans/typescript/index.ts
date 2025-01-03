import { dag, Directory, object, func } from "@dagger.io/dagger"
import { trace } from "@opentelemetry/api"

@object()
class MyModule {
  @func()
  async foo(): Promise<Directory> {
    const tracer = trace.getTracer("dagger-otel")

    // define the files to be created and their contents
    const files = {
      "file1.txt": "foo",
      "file2.txt": "bar",
      "file3.txt": "baz",
    }

    // set up an alpine container with the directory mounted
    let container = dag
      .container()
      .from("alpine:latest")
      .withDirectory("/results", dag.directory())
      .withWorkdir("/results")

    for (const [name, content] of Object.entries(files)) {
      // create a span for each file creation operation
      const span = tracer.startSpan("create-file", {
        attributes: {
          "file.name": name,
        },
      })
      // create the file and add it to the container
      container = container.withNewFile(name, content)
      // end the span
      span.end()
    }

    return container.directory("/results")
  }
}
