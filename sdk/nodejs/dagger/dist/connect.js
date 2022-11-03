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
import path from 'path';
import axios from 'axios';
/**
 * Cloak binary name
 */
const CLOAK_BINARY = "cloak";
/**
 * connect runs cloak GraphQL server and initializes a
 * Dagger client.
 * This implementation is based on the existing Go SDK.
 */
export function connect(config) {
    return __awaiter(this, void 0, void 0, function* () {
        // exit with error if we are not using the non-Cloak dagger binary (< 0.3.0)
        yield verifyCloakBinary();
        // Create config with default values that may be overridden
        // by config if values are set.
        const _config = Object.assign({ Workdir: process.env['DAGGER_WORKDIR'] || process.cwd(), ConfigPath: process.env['DAGGER_CONFIG'] || './dagger.json', Port: 8080, 
            // Set LocalDirs to {} so it's not null
            LocalDirs: {} }, config);
        const args = buildCLIArguments(_config);
        // Start Cloak server.
        const serverProcess = execa(CLOAK_BINARY, args, {
            stdio: "inherit",
            cwd: _config.Workdir
        });
        // Wait for Cloak server to be ready.
        yield waitCloakServer(_config.Port);
        // TODO(TomChv): How transform this part here? This function currently
        // execute the GQL query but instead we may want to returns a client and
        // close the server when it's finished.
        // The only way I see to do this is to let the client hold an optional server
        // process and shut it down in a Close function if it's defined.
        // WDYT?
        //
        // Piece of code to transform
        // ```
        //     await cb(new GraphQLClient(`http://localhost:${this.config.Port}/query`))
        //       .catch(async (err) => {
        //         // FIXME:(sipsma) give the engine a sec to flush any progress logs on error
        //         // Better solution is to send SIGTERM and have a handler in dagger engine that
        //         // flushes logs before exiting.
        //         await new Promise((resolve) => setTimeout(resolve, 1000));
        //         throw err;
        //       })
        //       .finally(async () => {
        //         serverProc.cancel();
        //         return serverProc.catch((e) => {
        //           if (!e.isCanceled) {
        //             console.error("dagger engine error: ", e);
        //           }
        //         });
        //       });
        // ```
        return new Client(_config.Port, serverProcess);
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
    const args = [
        'dev',
        '--workdir', `${opts.Workdir}`,
        '-p', `${opts.ConfigPath}`,
        '--port', `${opts.Port}`,
    ];
    // add local dirs from config in the form of `--local-dir <name>=<path>`
    for (const [name, localDir] of Object.entries(opts.LocalDirs)) {
        // If path is not absolute, we resolve it to its absolute path
        // This function do nothing if the path is already absolute
        const absoluteLocalDirPath = path.resolve(localDir);
        args.push('--local-dir', `${name}=${absoluteLocalDirPath}`);
    }
    return args;
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
