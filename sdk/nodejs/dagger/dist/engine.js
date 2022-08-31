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
export class Engine {
    constructor(config) {
        this.config = config;
    }
    run(cb) {
        return __awaiter(this, void 0, void 0, function* () {
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
                    yield client.get("/");
                }
                catch (e) {
                    yield new Promise((resolve) => setTimeout(resolve, 500));
                }
            }
            yield cb(new GraphQLClient(`http://localhost:${this.config.Port}`)).finally(() => __awaiter(this, void 0, void 0, function* () {
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
