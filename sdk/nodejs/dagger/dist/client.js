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
import { buildAxiosFetch } from "@lifeomic/axios-fetch";
import { GraphQLClient } from "graphql-request";
import { Response } from "node-fetch";
// @ts-expect-error node-fetch doesn't exactly match the Response object, but close enough.
global.Response = Response;
export const client = new GraphQLClient("http://fake.invalid/graphql", {
    fetch: buildAxiosFetch(axios.create({
        socketPath: "/dagger.sock",
        timeout: 3600e3,
    })),
});
export class Client {
    constructor() {
        this.client = axios.create({
            socketPath: "/dagger.sock",
            timeout: 3600e3,
        });
    }
    do(payload) {
        return __awaiter(this, void 0, void 0, function* () {
            const response = yield this.client.post(`http://fake.invalid/graphql`, payload, { headers: { "Content-Type": "application/graphql" } });
            return response;
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
