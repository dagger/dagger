var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import { getProvisioner, DEFAULT_HOST } from './provisioning/index.js';
/**
 * connect runs cloak GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
export function connect(cb, config = {}) {
    return __awaiter(this, void 0, void 0, function* () {
        // Create config with default values that may be overridden
        // by config if values are set.
        const _config = Object.assign({ Workdir: process.env['DAGGER_WORKDIR'] || process.cwd(), ConfigPath: process.env['DAGGER_CONFIG'] || './dagger.json' }, config);
        // set host to be DAGGER_HOST env otherwise to provisioning defaults
        const host = process.env['DAGGER_HOST'] || DEFAULT_HOST;
        const provisioner = getProvisioner(host);
        yield cb(yield provisioner.Connect(_config)).finally(() => __awaiter(this, void 0, void 0, function* () { return provisioner.Close(); }));
    });
}
