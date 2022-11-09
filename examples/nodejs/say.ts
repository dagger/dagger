import Client, {connect} from "@dagger.io/nodejs-sdk"

connect(async (client: Client) => {
  const ctr = client
    .container()
    .from({address: "node"})
    .exec({args: ["npm", "install", "-g", "cowsay"]})
    .withEntrypoint({args: ["cowsay"]})

  const result = await ctr.exec({args: process.argv.slice(2)}).stdout().contents()
  
  console.log(result.contents)
})