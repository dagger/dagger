import { ConnectOpts, EngineConn } from "../engineconn.js";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import readline from "readline";
import { execaCommandSync, execaCommand, ExecaChildProcess } from "execa";
import Client from "../../api/client.gen.js";

/**
 * HTTP is an implementation of EngineConn to connect to an existing
 * engine session over http.
 */
export class HTTP implements EngineConn {
  private url: URL;

  constructor(u: URL) {
    this.url = u;
  }

  Addr(): string {
    return this.url.toString();
  }

  async Connect(opts: ConnectOpts): Promise<Client> {
    return new Client({ host: this.url.host });
  }

  async Close(): Promise<void> {
    return;
  }
}
