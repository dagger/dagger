var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import axios from "axios";
import { execa } from "execa";
import { GraphQLClient } from "graphql-request";
import path from "path";
export class Engine {
    constructor(config) {
        this.config = config;
    }
    run(cb) {
        return __awaiter(this, void 0, void 0, function* () {
            const args = ["dev"];
            this.config = this.config || {};
            this.config.Workdir =
                this.config.Workdir || process.env["CLOAK_WORKDIR"] || process.cwd();
            args.push("--workdir", `${this.config.Workdir}`);
            this.config.ConfigPath =
                this.config.ConfigPath || process.env["CLOAK_CONFIG"] || "./cloak.yaml";
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
                cwd: this.config.Workdir,
            });
            // use axios-fetch to try connecting to the server until successful
            // FIXME:(sipsma) hardcoding that the server has 3 minutes to import+install all extensions...
            const client = axios.create({
                baseURL: `http://localhost:${this.config.Port}`,
            });
            for (let i = 0; i < 360; i++) {
                try {
                    yield client.get("/query");
                }
                catch (e) {
                    yield new Promise((resolve) => setTimeout(resolve, 500));
                }
            }
            yield cb(new GraphQLClient(`http://localhost:${this.config.Port}/query`))
                .catch((err) => __awaiter(this, void 0, void 0, function* () {
                // FIXME:(sipsma) give the engine a sec to flush any progress logs on error
                // Better solution is to send SIGTERM and have a handler in cloak engine that
                // flushes logs before exiting.
                yield new Promise((resolve) => setTimeout(resolve, 1000));
                throw err;
            }))
                .finally(() => __awaiter(this, void 0, void 0, function* () {
                serverProc.cancel();
                return serverProc.catch((e) => {
                    if (!e.isCanceled) {
                        console.error("cloak engine error: ", e);
                    }
                });
            }));
        });
    }
}
