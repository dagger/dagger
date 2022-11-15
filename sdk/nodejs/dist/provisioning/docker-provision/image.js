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
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import readline from 'readline';
import { execaCommandSync, execaCommand } from 'execa';
import Client from '../../api/client.js';
/**
 * ImageRef is a simple abstraction of docker image reference.
 */
class ImageRef {
    constructor(ref) {
        /**
         * trim image digests to 16 characters to make output more readable.
         */
        this.DIGEST_LEN = 16;
        // Throw error if ref is not correctly formatted.
        ImageRef.validate(ref);
        this.ref = ref;
        const id = ref.split('@sha256:', 2)[1];
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
     * is compliant with DockerImage constructor.
     *
     * This function does not return anything but
     * only throw on error.
     *
     * @throws no digest found in ref.
     */
    static validate(ref) {
        if (!ref.includes('@sha256')) {
            throw new Error(`no digest found in ref ${ref}`);
        }
    }
}
/**
 * DockerImage is an implementation of EngineConn to set up a Dagger
 * Engine session from a pulled docker image.
 */
export class DockerImage {
    constructor(u) {
        this.cacheDir = path.join(process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache'), 'dagger');
        this.ENGINE_SESSION_BINARY_PREFIX = 'dagger-engine-session';
        this.imageRef = new ImageRef(u.host + u.pathname);
    }
    Addr() {
        return 'http://dagger';
    }
    Connect(opts) {
        return __awaiter(this, void 0, void 0, function* () {
            this.createCacheDir();
            const engineSessionBinPath = this.buildBinPath();
            if (!fs.existsSync(engineSessionBinPath)) {
                this.pullEngineSessionBin(engineSessionBinPath);
            }
            return this.runEngineSession(engineSessionBinPath, opts);
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
            case 'win32':
                return `${binPath}.exe`;
            default:
                return binPath;
        }
    }
    /**
     * pullEngineSessionBin will retrieve Dagger binary from its docker image
     * and copy it to the local host.
     * This function automatically resolves host's platform to copy the correct
     * binary.
     */
    pullEngineSessionBin(engineSessionBinPath) {
        // Create a temporary bin file path
        const tmpBinPath = path.join(this.cacheDir, `temp-${this.ENGINE_SESSION_BINARY_PREFIX}`);
        const dockerRunArgs = [
            'docker',
            'run',
            '--rm',
            '--entrypoint',
            '/bin/cat',
            this.imageRef.Ref,
            `/usr/bin/${this.ENGINE_SESSION_BINARY_PREFIX}-${os.platform()}-${os.arch()}`,
        ];
        try {
            const fd = fs.openSync(tmpBinPath, 'w', 0o700);
            execaCommandSync(dockerRunArgs.join(' '), {
                stdout: fd,
                stderr: 'pipe',
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
        // Remove all temporary binary files
        // Ignore current engine session binary or other files that have not be
        // created by this SDK.
        try {
            const files = fs.readdirSync(this.cacheDir);
            files.forEach((file) => {
                const filePath = `${this.cacheDir}/${file}`;
                if (filePath === engineSessionBinPath || !file.startsWith(this.ENGINE_SESSION_BINARY_PREFIX)) {
                    return;
                }
                fs.unlinkSync(filePath);
            });
        }
        catch (e) {
            // Log the error but do not interrupt program.
            console.error('could not clean up temporary binary files');
        }
    }
    /**
     * runEngineSession execute the engine binary and set up a GraphQL client that
     * target this engine.
     */
    runEngineSession(engineSessionBinPath, opts) {
        var e_1, _a;
        return __awaiter(this, void 0, void 0, function* () {
            const engineSessionArgs = [engineSessionBinPath, '--remote', `docker-image://${this.imageRef.Ref}`];
            if (opts.Workdir) {
                engineSessionArgs.push('--workdir', opts.Workdir);
            }
            if (opts.Project) {
                engineSessionArgs.push('--project', opts.Project);
            }
            const { stdout, pid } = execaCommand(engineSessionArgs.join(' '), {
                stderr: opts.OutputLog || process.stderr,
                // Kill the process if parent exit.
                cleanup: true,
            });
            // Register PID to kill it later.
            this.enginePid = pid;
            const stdoutReader = readline.createInterface({
                input: stdout,
            });
            // Set a timeout of 10 seconds by default
            // Do not call the function if port is successfully retrieved.
            const timeoutFct = setTimeout(() => __awaiter(this, void 0, void 0, function* () { return this.Close(); }), opts.Timeout || 10000);
            try {
                for (var stdoutReader_1 = __asyncValues(stdoutReader), stdoutReader_1_1; stdoutReader_1_1 = yield stdoutReader_1.next(), !stdoutReader_1_1.done;) {
                    const line = stdoutReader_1_1.value;
                    // Read line as a port number
                    const port = parseInt(line);
                    clearTimeout(timeoutFct);
                    return new Client({ port: port });
                }
            }
            catch (e_1_1) { e_1 = { error: e_1_1 }; }
            finally {
                try {
                    if (stdoutReader_1_1 && !stdoutReader_1_1.done && (_a = stdoutReader_1.return)) yield _a.call(stdoutReader_1);
                }
                finally { if (e_1) throw e_1.error; }
            }
            throw new Error('failed to connect to engine session');
        });
    }
    Close() {
        return __awaiter(this, void 0, void 0, function* () {
            if (this.enginePid) {
                process.kill(this.enginePid);
            }
        });
    }
}
