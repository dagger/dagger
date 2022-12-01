import Client, {connect} from "@dagger.io/dagger"

connect(async (client: Client) => {
  const ctr = client
    .container()
    .from("node")
    .withExec(["npm", "install", "-g", "cowsay"])
    .withEntrypoint(["cowsay"])

  const result = await ctr.withExec(process.argv.slice(2)).stdout()

  console.log(result)
})
