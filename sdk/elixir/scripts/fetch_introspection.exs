query = File.read!("dagger_codegen/priv/introspection.graphql")
{:ok, client} = Dagger.Core.Client.connect(connect_timeout: :timer.seconds(50))
{:ok, resp} = Dagger.Core.Client.query(client, query)
File.write!("introspection.json", Jason.encode!(resp["data"]))
