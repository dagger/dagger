import axios from "axios";
import { execa } from "execa";
import { GraphQLClient } from "graphql-request";

export interface EngineOptions {
  LocalDirs?: Record<string, string>;
  Port?: number;
}

export class Engine {
  private config: EngineOptions;

  constructor(config: EngineOptions) {
    this.config = config;
  }

  async run(cb: (client: GraphQLClient) => Promise<void>) {
    const args = ["dev"];
    // add local dirs from config in the form of `--local-dir <name>=<path>`
    if (this.config.LocalDirs) {
      for (const [name, path] of Object.entries(this.config.LocalDirs)) {
        args.push("--local-dir", `${name}=${path}`);
      }
    }
    // add port from config in the form of `--port <port>`, defaulting to 8080
    if (!this.config.Port) {
      this.config.Port = 8080;
    }
    args.push("--port", `${this.config.Port}`);

    const serverProc = execa("cloak", args, {
      stdio: "inherit",
    });

    // use axios-fetch to try connecting to the server until successful
    // FIXME:(sipsma) hardcoding that the server has 60 seconds to import+install all extensions...
    const client = axios.create({
      baseURL: `http://localhost:${this.config.Port}`,
    });
    for (let i = 0; i < 120; i++) {
      try {
        await client.get("/");
      } catch (e) {
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
    }

    await cb(new GraphQLClient(`http://localhost:${this.config.Port}`)).finally(
      async () => {
        serverProc.cancel();
        return serverProc.catch((e) => {
          if (!e.isCanceled) {
            console.error("cloak engine error: ", e);
          }
        });
      }
    );
  }
}
