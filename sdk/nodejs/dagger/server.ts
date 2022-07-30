import { Client } from "./client";

import * as fs from "fs";

export interface DaggerContext {
  dagger: Client;
}

export class DaggerServer {
  // TODO: tighten up resolvers type?
  resolvers: Record<string, any>;

  constructor(config: { resolvers: Record<string, any> }) {
    this.resolvers = config.resolvers;
  }

  public run() {
    const input = JSON.parse(fs.readFileSync("/inputs/dagger.json", "utf8"));

    var obj: string = input.object;
    if (obj === undefined) {
      throw new Error("No object found in input");
    }
    obj = obj.charAt(0).toUpperCase() + obj.slice(1);

    const args = input.args;
    if (args === undefined) {
      throw new Error("No args found in input");
    }

    (async () =>
      // TODO: handle parent, context, info
      await this.resolvers[obj](args).then((result: any) => {
        console.log(result);
        if (result === undefined) {
          result = {};
        }
        fs.writeFileSync("/outputs/dagger.json", JSON.stringify(result));
      }))();
  }
}
