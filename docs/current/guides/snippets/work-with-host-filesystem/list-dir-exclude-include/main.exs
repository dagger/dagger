Mix.install([:dagger])

tmp_dir = System.tmp_dir!()
target = Path.join([tmp_dir, "out"])

File.mkdir_p!(target)
File.write!(Path.join([target, "foo.txt"]), "1")
File.write!(Path.join([target, "bar.txt"]), "2")
File.write!(Path.join([target, "bar.rar"]), "3")

client = Dagger.connect!(workdir: target)

{:ok, entries} =
  client
  |> Dagger.Client.host()
  |> Dagger.Host.directory(".", include: ["*.*"], exclude: ["*.rar"])
  |> Dagger.Directory.entries()

IO.inspect(entries)

Dagger.close(client)
