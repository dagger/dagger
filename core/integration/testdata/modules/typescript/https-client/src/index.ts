import { object, func } from "@dagger.io/dagger"
import * as https from "https"

@object()
export class Test {
  @func()
  async getHttp(): Promise<string> {
    return new Promise((resolve, reject) => {
      https
        .get("https://server", (res) => {
          let data = ""
          res.on("data", (chunk) => {
            data += chunk
          })
          res.on("end", () => {
            if (res.statusCode === 200) {
              resolve(data)
              return
            }
            reject(new Error(`Request failed with status code ${res.statusCode}`))
          })
        })
        .on("error", (err) => {
          reject(new Error(`Error: ${err.message}`))
        })
    })
  }
}
