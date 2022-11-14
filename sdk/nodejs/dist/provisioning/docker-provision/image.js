var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
var __asyncValues = (this && this.__asyncValues) || function (o) {
    if (!Symbol.asyncIterator) throw new TypeError("Symbol.asyncIterator is not defined.");
    var m = o[Symbol.asyncIterator], i;
    return m ? m.call(o) : (o = typeof __values === "function" ? __values(o) : o[Symbol.iterator](), i = {}, verb("next"), verb("throw"), verb("return"), i[Symbol.asyncIterator] = function () { return this; }, i);
    function verb(n) { i[n] = o[n] && function (v) { return new Promise(function (resolve, reject) { v = o[n](v), settle(resolve, reject, v.done, v.value); }); }; }
    function settle(resolve, reject, d, v) { Promise.resolve(v).then(function(v) { resolve({ value: v, done: d }); }, reject); }
};
import { GraphQLClient } from "graphql-request";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import readline from "readline";
import { execaCommandSync, execaCommand } from "execa";
class ImageRef {
    constructor(ref) {
        /**
         * trim image digests to 16 characters to make output more readable
         */
        this.DIGEST_LEN = 16;
        // Throw error if ref is not correctly formatted
        ImageRef.validate(ref);
        this.ref = ref;
        const id = ref.split("@sha256:", 2)[1];
        this.id = id.slice(0, this.DIGEST_LEN);
    }
    get Ref() {
        return this.ref;
    }
    get ID() {
        return this.id;
    }
    /**
     * validateImage verify that the passed ref
     * is compliant with DockerImage constructor
     *
     * This function does not return anything but
     * only throw on error.
     *
     * @throws no digest found in ref
     */
    static validate(ref) {
        if (!ref.includes("@sha256")) {
            throw new Error(`no digest found in ref ${ref}`);
        }
    }
}
export class DockerImage {
    constructor(u) {
        this.cacheDir = path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache"), "dagger");
        this.ENGINE_SESSION_BINARY_PREFIX = "dagger-engine-session";
        this.imageRef = new ImageRef(u.host + u.pathname);
    }
    Addr() {
        return "http://dagger";
    }
    Connect(opts) {
        var e_1, _a;
        return __awaiter(this, void 0, void 0, function* () {
            this.createCacheDir();
            const engineSessionBinPath = this.buildBinPath();
            if (!fs.existsSync(engineSessionBinPath)) {
                this.pullEngineSessionBin(engineSessionBinPath);
            }
            const remote = "docker-image://" + this.imageRef.Ref;
            var engineSessionArgs = [engineSessionBinPath, "--remote", remote];
            if (opts.Workdir) {
                engineSessionArgs.push("--workdir", opts.Workdir);
            }
            if (opts.ConfigPath) {
                engineSessionArgs.push("--project", opts.ConfigPath);
            }
            const commandOpts = {
                stderr: process.stderr,
                // Kill the process if parent exit.
                cleanup: true,
            };
            const cmd = execaCommand(engineSessionArgs.join(" "), commandOpts);
            const stdoutReader = readline.createInterface({
                input: cmd.stdout,
            });
            var port;
            try {
                // TODO: timeout here
                for (var stdoutReader_1 = __asyncValues(stdoutReader), stdoutReader_1_1; stdoutReader_1_1 = yield stdoutReader_1.next(), !stdoutReader_1_1.done;) {
                    const line = stdoutReader_1_1.value;
                    // read line as a port number
                    port = parseInt(line);
                    return new GraphQLClient(`http://127.0.0.1:${port}/query`);
                }
            }
            catch (e_1_1) { e_1 = { error: e_1_1 }; }
            finally {
                try {
                    if (stdoutReader_1_1 && !stdoutReader_1_1.done && (_a = stdoutReader_1.return)) yield _a.call(stdoutReader_1);
                }
                finally { if (e_1) throw e_1.error; }
            }
            throw new Error("failed to connect to engine session");
        });
    }
    /**
     * createCacheDir will create a cache directory on user
     * host to store dagger binary.
     *
     * If set, it will use XDG directory, if not, it will use `$HOME/.cache`
     * as base path.
     * Nothing happens if the directory already exists.
     */
    createCacheDir() {
        fs.mkdirSync(this.cacheDir, { mode: 0o700, recursive: true });
    }
    /**
     * buildBinPath create a path to output engine session binary.
     *
     * It will store it in the cache directory with a name composed
     * of the base engine session as constant and the engine identifier.
     */
    buildBinPath() {
        const binPath = `${this.cacheDir}/${this.ENGINE_SESSION_BINARY_PREFIX}-${this.imageRef.ID}`;
        switch (os.platform()) {
            case "win32":
                return `${binPath}.exe`;
            default:
                return binPath;
        }
    }
    pullEngineSessionBin(engineSessionBinPath) {
        // Create a temporary bin file
        const tmpBinPath = path.join(this.cacheDir, `temp-${this.ENGINE_SESSION_BINARY_PREFIX}`);
        const dockerRunArgs = [
            "docker",
            "run",
            "--rm",
            "--entrypoint",
            "/bin/cat",
            this.imageRef.Ref,
            `/usr/bin/${this.ENGINE_SESSION_BINARY_PREFIX}-${os.platform()}-${os.arch()}`,
        ];
        try {
            const fd = fs.openSync(tmpBinPath, "w", 0o700);
            const process = execaCommandSync(dockerRunArgs.join(" "), {
                stdout: fd,
                stderr: "pipe",
                encoding: null,
                // Kill the process if parent exit.
                cleanup: true,
                // Throw on error
                reject: false,
                timeout: 300000,
            });
            fs.closeSync(fd);
            fs.renameSync(tmpBinPath, engineSessionBinPath);
        }
        catch (e) {
            fs.rmSync(tmpBinPath);
            throw new Error(`failed to copy engine session binary: ${e}`);
        }
        // TODO: garbage collect older binaries
    }
    Close() {
        return __awaiter(this, void 0, void 0, function* () {
            return Promise.resolve(undefined);
        });
    }
}
