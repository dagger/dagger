Mix.install([:dagger])

client = Dagger.connect!()

project =
  client
  |> Dagger.Client.git("https://github.com/dagger/dagger")
  |> Dagger.GitRepository.branch("main")
  |> Dagger.GitRef.tree()

{:ok, contents} =
  client
  |> Dagger.Client.container()
  |> Dagger.Container.from("alpine:latest")
  |> Dagger.Container.with_directory("/src", project, exclude: ["*.md"])
  |> Dagger.Container.with_workdir("/src")
  |> Dagger.Container.with_exec(["ls", "/src"])
  |> Dagger.Container.stdout()

IO.puts(contents)

Dagger.close(client)
