var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import axios from 'axios';
import { buildAxiosFetch } from '@lifeomic/axios-fetch';
import { GraphQLClient } from 'graphql-request';
import { Response } from 'node-fetch';
// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;
export const client = new GraphQLClient('http://fake.invalid/query', {
    fetch: buildAxiosFetch(axios.create({
        socketPath: '/dagger.sock',
        timeout: 3600e3,
    })),
});
export class Client {
    /**
     * creates a new Dagger Typescript SDK GraphQL client.
     * If the client is created by `dagger.connect()`, it will
     * hold the serverProcess, so it can be closed using `close()`
     * method.
     */
    constructor(port = 8080, serverProcess) {
        this.client = new GraphQLClient(`http://localhost:${port}/query`);
        this.serverProcess = serverProcess;
    }
    /**
     * do takes a GraphQL query payload as parameter and send it
     * to Cloak server to execute every operation's in it.
     */
    do(payload) {
        return __awaiter(this, void 0, void 0, function* () {
            return yield this.client.request(payload);
        });
    }
    /**
     * close will stop the server process if it has been launched by
     * the Typescript SDK.
     */
    close() {
        return __awaiter(this, void 0, void 0, function* () {
            if (!this.serverProcess) {
                return;
            }
            this.serverProcess.cancel();
            this.serverProcess.catch((e) => {
                if (!e.isCanceled) {
                    console.error('dagger engine error: ', e);
                }
            });
        });
    }
}
export class FSID {
    constructor(serial) {
        this.serial = serial;
    }
    toString() {
        return this.serial;
    }
    toJSON() {
        return this.serial;
    }
}
export class SecretID {
    constructor(serial) {
        this.serial = serial;
    }
    toString() {
        return this.serial;
    }
    toJSON() {
        return this.serial;
    }
}
