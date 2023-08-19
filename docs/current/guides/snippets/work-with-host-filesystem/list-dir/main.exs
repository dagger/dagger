Mix.install([:dagger])

client = Dagger.connect!()

{:ok, entries} =
  client
  |> Dagger.Client.host()
  |> Dagger.Host.directory(".")
  |> Dagger.Directory.entries()

IO.inspect(entries)

Dagger.close(client)
