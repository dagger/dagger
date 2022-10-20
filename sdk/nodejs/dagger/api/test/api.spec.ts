import Api from "../api.js"

describe('NodeJS SDK api', function () {
  it.only('Get a the workdir id', async function () {
    this.timeout(60000);
    const tree = await new Api().host().workdir().read().id()
    console.log("🚀 ~ file: api.spec.ts ~ line 9 ~ tree", tree)
  })
  
  it('Get a Dagger Github Readme', async function () {
    this.timeout(60000);
    const tree = await new Api().container().from({address: "alpine"}).exec({args: ["apk", "add", "curl"]}).stdout().id()
    console.log("🚀 ~ file: api.spec.ts ~ line 19 ~ tree", tree)
  })
});