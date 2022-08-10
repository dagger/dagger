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

    var resolverName: string = input.resolver;
    if (resolverName === undefined) {
      throw new Error("No resolverName found in input");
    }
    const nameSplit = resolverName.split(".");
    const objName = nameSplit[0];
    const fieldName = nameSplit[1];

    const args = input.args;
    if (args === undefined) {
      throw new Error("No args found in input");
    }

    (async () =>
      // TODO: handle parent, context, info
      await this.resolvers[objName][fieldName](args).then((result: any) => {
        if (result === undefined) {
          result = {};
        }
        fs.writeFileSync("/outputs/dagger.json", JSON.stringify(result));
      }))();
  }
}
