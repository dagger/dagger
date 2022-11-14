var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import { GraphQLClient } from "graphql-request";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import { execaCommandSync } from "execa";
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
        return __awaiter(this, void 0, void 0, function* () {
            this.createCacheDir();
            const engineSessionBinPath = this.buildBinPath();
            if (!fs.existsSync(engineSessionBinPath)) {
                this.pullEngineSessionBin();
            }
            return new GraphQLClient(`http://localhost:${opts.Port}/query`);
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
    pullEngineSessionBin() {
        // Create a temporary bin file
        const tmpBinPath = path.join(this.cacheDir, `temp-${this.ENGINE_SESSION_BINARY_PREFIX}`);
        console.log(`${this.ENGINE_SESSION_BINARY_PREFIX}-${os.platform()}-${os.arch()}`);
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
                timeout: 10000,
            });
        }
        catch (e) {
            console.log(e);
            fs.rmSync(tmpBinPath);
            throw new Error(`failed to copy engine session binary: ${e}`);
        }
        fs.chmodSync(tmpBinPath, 0o700);
        return tmpBinPath;
    }
    Close() {
        return __awaiter(this, void 0, void 0, function* () {
            return Promise.resolve(undefined);
        });
    }
}
