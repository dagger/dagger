Mix.install([:dagger])

client = Dagger.connect!()

host_dir =
  client
  |> Dagger.Client.host()
  |> Dagger.Host.directory(".")

{:ok, out} =
  client
  |> Dagger.Client.container()
  |> Dagger.Container.from("alpine:latest")
  |> Dagger.Container.with_directory("/host", host_dir)
  |> Dagger.Container.with_exec(["ls", "/host"])
  |> Dagger.Container.stdout()

IO.puts(out)

Dagger.close(client)
