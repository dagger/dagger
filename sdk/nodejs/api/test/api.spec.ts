import assert from "assert";
import Api from "../client.js"
import { queryBuilder, queryFlatten } from "../utils.js"

describe('NodeJS SDK api', function () {
  it('Build correctly a query with one argument', async function () {

    const tree = new Api().container().from({address: "alpine"})

    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine")}}`);
  })
  
  it('Build one query with multiple arguments', async function () {
    const tree = new Api().container().from({address: "alpine"}).exec({args: ["apk", "add", "curl"]}).stdout()
    
    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine"){exec(args:["apk","add","curl"]){stdout}}}}`);
  })

  it('Build a query by splitting it', async function () {
    const image = new Api().container().from({address: "alpine"})
    const pkg = image.exec({args: ["apk", "add", "curl"]})
    const result = pkg.stdout()
    
    assert.strictEqual(queryBuilder(result.queryTree), `{container{from(address:"alpine"){exec(args:["apk","add","curl"]){stdout}}}}`);
  })

  it('Return a flatten Graphql response', async function () {
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