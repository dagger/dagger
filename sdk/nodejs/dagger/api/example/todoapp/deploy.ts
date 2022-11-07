// import { NetlifyAPI } from "netlify";
import Client from "../../api.js"
import { connect } from "../../../connect.js"


const netlifyToken = "-dhS2kFfj75MM5Dh3hhzU3fyTN3vHuouV84_nhuys10"
// const netlifySiteName = "dagger-io-with-sdk"

// if (!netlifyToken) {
//   console.log(
//     "Missing netlify API token. Please set it to the env variable $NETLIFY_AUTH_TOKEN"
//   );
//   process.exit(1);
// }


connect(async (client: Client) => {

  const source = await client.host().workdir().id()

  const cacheVolume = await client.cacheVolume({key: "myCache"}).id()

  const image = await client.container().from({address: "alpine"}).exec({args: ["apk", "add", "yarn"]}).id()

  await client
    .container({id: image.id})
    .withMountedDirectory({path: "/src", source: source.id})
    .withWorkdir({path: "/src"})
    .withEnvVariable({name: "YARN_CACHE_FOLDER", value: "/cache"})
    .withMountedCache({path: "/cache", cache: cacheVolume.id, source: source.id})
    .exec({args: ["yarn", "install"]})
    .exec({args: ["yarn", "build"]})
    .directory({path: "/src"}).entries()
})

