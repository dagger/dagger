// import { NetlifyAPI } from "netlify";
import Api from "../../api.js"
import { connect } from "../../../_engine.js"


const netlifyToken = "-dhS2kFfj75MM5Dh3hhzU3fyTN3vHuouV84_nhuys10"
// const netlifySiteName = "dagger-io-with-sdk"

// if (!netlifyToken) {
//   console.log(
//     "Missing netlify API token. Please set it to the env variable $NETLIFY_AUTH_TOKEN"
//   );
//   process.exit(1);
// }


connect(async (api: Api) => {

  const source = await api.host().workdir().id()

  const cacheVolume = await api.cacheVolume({key: "myCache"}).id()

  const image = await api.container().from({address: "alpine"}).exec({args: ["apk", "add", "yarn"]}).id()

  await api
    .container({id: image.id})
    .withMountedDirectory({path: "/src", source: source.id})
    .withWorkdir({path: "/src"})
    .withEnvVariable({name: "YARN_CACHE_FOLDER", value: "/cache"})
    .withMountedCache({path: "/cache", cache: cacheVolume.id, source: source.id})
    .exec({args: ["yarn", "install"]})
    .exec({args: ["yarn", "build"]})
    .directory({path: "/src"}).entries()
})

// // 1. Load app source code from working directory
// const source = await new Api().host().workdir().id()


//   // 2. Install yarn in a container
// const image = await new Api()
// .container()
// .from({address: "alpine"})
// .exec({args: ["apk", "add", "yarn"]})
// .fs()
// .id()

//   // 3. Run 'yarn install' in a container
// const sourceAfterInstall = await new Api()
//   .container({id: image.id})
//   .exec({args: ["yarn", "install"]})
//   .withMountedDirectory({path: "/src", source: source.id})
//   .withWorkdir({path: "/src"})
//   .directory({path: "/src"})

// console.log("🐞 ------------------------------------------🐞")
// console.log("🐞 ~ sourceAfterInstall", sourceAfterInstall)
// console.log("🐞 ------------------------------------------🐞")

