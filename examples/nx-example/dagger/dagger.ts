// import {connect, Client} from "@dagger.io/nodejs-sdk"



// connect(async (client:  Client) => {
//   const ctr = client
//     .container()
//     .from({address: "node"})
//     .withEntrypoint({args: ["cowsay"]})

//   const result = await ctr.exec({args: process.argv.slice(2)}).stdout().contents()
  
//   console.log(result.contents)
// })

console.log(process.cwd())