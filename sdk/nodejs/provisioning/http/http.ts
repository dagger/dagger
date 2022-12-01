import Client from "../../api/client.gen.js"
import { ConnectOpts, EngineConn } from "../engineconn.js"

/**
 * HTTP is an implementation of EngineConn to connect to an existing
 * engine session over http.
 */
export class HTTP implements EngineConn {
  private url: URL

  constructor(u: URL) {
    this.url = u
  }

  Addr(): string {
    return this.url.toString()
  }

  async Connect(_: ConnectOpts): Promise<Client> {
    return new Client({ host: this.url.host })
  }

  async Close(): Promise<void> {
    return
  }
}
