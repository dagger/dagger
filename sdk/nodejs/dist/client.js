import axios from 'axios';
import { buildAxiosFetch } from '@lifeomic/axios-fetch';
import { GraphQLClient } from 'graphql-request';
import { Response } from 'node-fetch';
// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;
// TODO(Tomchv): useless so we can remove it later.
export const client = new GraphQLClient('http://fake.invalid/query', {
    fetch: buildAxiosFetch(axios.create({
        socketPath: '/dagger.sock',
        timeout: 3600e3,
    })),
});
export class Client {
    /**
     * creates a new Dagger Typescript SDK GraphQL client.
     */
    constructor(port = 8080) {
        this.client = new GraphQLClient(`http://localhost:${port}/query`);
    }
    /**
     * do takes a GraphQL query payload as parameter and send it
     * to Cloak server to execute every operation's in it.
     */
    async do(payload) {
        return await this.client.request(payload);
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
