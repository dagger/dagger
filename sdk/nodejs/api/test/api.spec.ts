import assert from 'assert';
import Client from "../client.gen.js"
import { queryBuilder, queryFlatten } from "../utils.js"

describe('NodeJS SDK api', function () {
  
  it('Build correctly a query with one argument', async function () {
    const tree = new Client().container().from("alpine")

    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine")}}`);
  })
  
  it('Build one query with multiple arguments', async function () {
    const tree = new Client().container().from("alpine").exec(["apk", "add", "curl"]).stdout()
    
    assert.strictEqual(queryBuilder(tree.queryTree), `{container{from(address:"alpine"){exec(args:["apk","add","curl"]){stdout}}}}`);
  })

  it('Build a query by splitting it', async function () {
    const image = new Client().container().from("alpine")
    const pkg = image.exec(["apk", "add", "curl"])
    const result = pkg.stdout()
    
    assert.strictEqual(queryBuilder(result.queryTree), `{container{from(address:"alpine"){exec(args:["apk","add","curl"]){stdout}}}}`);
  })
  
  it('Test Field Immutability', async function () {
    const image = new Client().container().from("alpine")
    const a = image.exec(["echo","hello","world"])
    assert.strictEqual(queryBuilder(a.queryTree), `{container{from(address:"alpine"){exec(args:["echo","hello","world"])}}}`);
    const b = image.exec(["echo","foo","bar"])
    assert.strictEqual(queryBuilder(b.queryTree), `{container{from(address:"alpine"){exec(args:["echo","foo","bar"])}}}`);
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
