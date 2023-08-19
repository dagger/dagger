Mix.install([:dagger])

client = Dagger.connect!()

host_dir =
  client
  |> Dagger.Client.host()
  |> Dagger.Host.directory("/tmp/sandbox")

client
|> Dagger.Client.container()
|> Dagger.Container.from("alpine:latest")
|> Dagger.Container.with_directory("/host", host_dir)
|> Dagger.Container.with_exec(["/bin/sh", "-c", "`echo foo > /host/bar`"])
|> Dagger.Container.directory("/host")
|> Dagger.Directory.export("/tmp/sandbox")

Dagger.close(client)
