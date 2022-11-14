import axios from "axios";
import { execa, execaCommandSync } from "execa";
import { GraphQLClient } from "graphql-request";
import path from "path";

export interface EngineOptions {
  LocalDirs?: Record<string, string>;
  Port?: number;
  Workdir?: string;
  ConfigPath?: string;
}

export class Engine {
  private config: EngineOptions;

  constructor(config: EngineOptions) {
    this.config = config;
  }

  async run(cb: (client: GraphQLClient) => Promise<void>) {
    const args = ["dev"];

    // exit with error if we are not using the non-Cloak dagger binary (< 0.3.0)
    try {
      execaCommandSync("cloak dev --help");
    } catch (err) {
      console.error("⚠️  Please ensure that cloak binary in $PATH is v0.3.0 or newer.");
      // https://tldp.org/LDP/abs/html/exitcodes.html
      // | 127 | "command not found" | illegal_command | Possible problem with $PATH or a typo |
      process.exit(127);
    }

    this.config = this.config || {};

    this.config.Workdir =
      this.config.Workdir || process.env["DAGGER_WORKDIR"] || process.cwd();
    args.push("--workdir", `${this.config.Workdir}`);

    this.config.ConfigPath =
      this.config.ConfigPath || process.env["DAGGER_CONFIG"] || "./dagger.json";
    args.push("-p", `${this.config.ConfigPath}`);

    // add local dirs from config in the form of `--local-dir <name>=<path>`
    if (this.config.LocalDirs) {
      for (var [name, localDir] of Object.entries(this.config.LocalDirs)) {
        if (!path.isAbsolute(localDir)) {
          localDir = path.resolve(localDir);
        }
        args.push("--local-dir", `${name}=${localDir}`);
      }
    }
    // add port from config in the form of `--port <port>`, defaulting to 8080
    this.config.Port = this.config.Port || 8080;
    args.push("--port", `${this.config.Port}`);

    const serverProc = execa("cloak", args, {
      stdio: "inherit",
      cwd: this.config.Workdir
    });
    // use axios-fetch to try connecting to the server until successful
    // FIXME:(sipsma) hardcoding that the server has 3 minutes to import+install all extensions...
    const client = axios.create({
      baseURL: `http://localhost:${this.config.Port}`,
    });
    for (let i = 0; i < 360; i++) {
      try {
        await client.get("/query");
      } catch (e) {
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
    };
    await cb(new GraphQLClient(`http://localhost:${this.config.Port}/query`))
      .catch(async (err) => {
        // FIXME:(sipsma) give the engine a sec to flush any progress logs on error
        // Better solution is to send SIGTERM and have a handler in dagger engine that
        // flushes logs before exiting.
        await new Promise((resolve) => setTimeout(resolve, 1000));
        throw err;
      })
      .finally(async () => {
        serverProc.cancel();
        return serverProc.catch((e) => {
          if (!e.isCanceled) {
            console.error("dagger engine error: ", e);
          }
        });
      });
  }
}
