var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import * as fs from "fs";
export class DaggerServer {
    constructor(config) {
        this.resolvers = config.resolvers;
    }
    run() {
        const input = JSON.parse(fs.readFileSync("/inputs/dagger.json", "utf8"));
        const resolverName = input.resolver;
        if (resolverName === undefined) {
            throw new Error("No resolverName found in input");
        }
        const nameSplit = resolverName.split(".");
        const objName = nameSplit[0];
        const fieldName = nameSplit[1];
        const args = input.args;
        if (args === undefined) {
            throw new Error("No args found in input");
        }
        const parent = input.parent;
        let objectResolvers = this.resolvers[objName];
        if (!objectResolvers) {
            objectResolvers = {};
        }
        let resolver = objectResolvers[fieldName];
        if (!resolver) {
            // default to the graphql trivial resolver implementation
            resolver = (_, parent) => __awaiter(this, void 0, void 0, function* () {
                if (parent === null || parent === undefined) {
                    return {};
                }
                return parent[fieldName];
            });
        }
        (() => __awaiter(this, void 0, void 0, function* () {
            // TODO: handle context, info?
            return yield resolver(args, parent).then((result) => {
                if (result === undefined) {
                    result = {};
                }
                fs.writeFileSync("/outputs/dagger.json", JSON.stringify(result));
            });
        }))();
    }
}
