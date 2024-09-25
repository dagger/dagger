import { dag, object, Directory, Container, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async foo(): Container {
<<<<<<< HEAD
<<<<<<< HEAD
    return await dag
=======
		return await dag
>>>>>>> 2f22413a8 (Fix linter)
=======
    return await dag
>>>>>>> 732cb55c8 (Fix linter)
      .container()
      .from("alpine:latest")
      .terminal()
      .withExec(["sh", "-c", "echo hello world > /foo"])
      .terminal()
  }
}
