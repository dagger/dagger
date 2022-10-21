import assert from "assert";
import Api from "../api.js"
import { queryBuilder, queryFlatten } from "../utils.js"

describe('NodeJS SDK api', function () {
  it('Build correctly a query with one argument', async function () {
    this.timeout(60000);

    const tree = await new Api().container().from({address: "alpine"})

    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine")}}`);
  })
  
  it('Build correctly a query with multiple arguments', async function () {
    this.timeout(60000);
    const tree = await new Api().container().from({address: "alpine"}).exec({args: ["apk", "add", "curl"]}).stdout()
    
    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine"){exec(args:["apk","add","curl"]){stdout}}}}`);
  })

  it('Return a flatten Graphql response', async function () {
    this.timeout(60000);
    const tree = {
                    container: {
                      from: {
                        exec: {
                          stdout: {
                            contents: 'fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz'
                          }
                        }
                      }
                    }
                  }

    assert.deepStrictEqual(queryFlatten(tree), {
          contents: 'fetch https://dl-cdn.alpinelinux.org/alpine/v3.16/main/aarch64/APKINDEX.tar.gz'
        });
  })
});