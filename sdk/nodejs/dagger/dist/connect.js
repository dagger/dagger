var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
// WIP(TomChv): This file shall be renamed to something else
import { Client } from './client.js';
import { execa, execaCommandSync } from 'execa';
import axios from 'axios';
/**
 * Cloak binary name
 */
const CLOAK_BINARY = "cloak";
/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export function connect(cb, config = {}) {
    return __awaiter(this, void 0, void 0, function* () {
        // exit with error if we are not using the non-Cloak dagger binary (< 0.3.0)
        yield verifyCloakBinary();
        // Create config with default values that may be overridden
        // by config if values are set.
        const _config = Object.assign({ Workdir: process.env['DAGGER_WORKDIR'] || process.cwd(), ConfigPath: process.env['DAGGER_CONFIG'] || './dagger.json', Port: 8080 }, config);
        const args = buildCLIArguments(_config);
        // Start Cloak server.
        const serverProcess = execa(CLOAK_BINARY, args, {
            stdio: "inherit",
            cwd: _config.Workdir
        });
        // Wait for Cloak server to be ready.
        yield waitCloakServer(_config.Port);
        // Execute users workflow and shutdown the server at the end of the
        // execution. Either it succeeds or fails.
        yield cb(new Client(_config.Port))
            .finally(() => __awaiter(this, void 0, void 0, function* () {
            serverProcess.cancel();
            yield serverProcess.catch((e) => {
                if (!e.isCanceled) {
                    console.error('dagger engine error: ', e);
                }
            });
        }));
    });
}
/**
 * verifyCloakBinary make sure that user is using the correct version of
 * Dagger (> 0.3.0).
 * This function exits on error.
 */
function verifyCloakBinary() {
    return __awaiter(this, void 0, void 0, function* () {
        try {
            execaCommandSync('cloak dev --help');
        }
        catch (err) {
            console.error('⚠️  Please ensure that cloak binary in $PATH is v0.3.0 or newer.');
            // https://tldp.org/LDP/abs/html/exitcodes.html
            // | 127 | "command not found" | illegal_command | Possible problem with $PATH or a typo |
            process.exit(127);
        }
    });
}
/**
 * buildCLIArguments creates a list of string that correspond to
 * the arguments concatenated to "cloak dev" command.
 */
function buildCLIArguments(opts) {
    return [
        'dev',
        '--workdir', `${opts.Workdir}`,
        '-p', `${opts.ConfigPath}`,
        '--port', `${opts.Port}`,
    ];
}
/**
 * waitCloakServer use an axios client to try connecting to the cloak server
 * until it successfully connect.
 * This function has a timeout of 3 minutes to imports and installs all
 * extensions.
 */
function waitCloakServer(port) {
    return __awaiter(this, void 0, void 0, function* () {
        const client = axios.create({
            baseURL: `http://localhost:${port}`,
        });
        // Wait 500ms between each attempt.
        for (let i = 0; i < 360; i++) {
            try {
                yield client.get("/query");
            }
            catch (e) {
                yield new Promise((resolve) => setTimeout(resolve, 500));
            }
        }
    });
}
