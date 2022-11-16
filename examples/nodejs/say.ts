import Client, {connect} from "@dagger.io/dagger"

connect(async (client: Client) => {
  const ctr = client
    .container()
    .from("node")
    .exec(["npm", "install", "-g", "cowsay"])
    .withEntrypoint(["cowsay"])

  const result = await ctr.exec(process.argv.slice(2)).stdout().contents()

  console.log(result.contents)
})
