import * as fs from "fs";
export class DaggerServer {
    constructor(config) {
        this.resolvers = config.resolvers;
    }
    run() {
        const input = JSON.parse(fs.readFileSync("/inputs/dagger.json", "utf8"));
        var resolverName = input.resolver;
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
            resolver = async (_, parent) => {
                if (parent === null || parent === undefined) {
                    return {};
                }
                return parent[fieldName];
            };
        }
        (async () => 
        // TODO: handle context, info?
        await resolver(args, parent).then((result) => {
            if (result === undefined) {
                result = {};
            }
            fs.writeFileSync("/outputs/dagger.json", JSON.stringify(result));
        }))();
    }
}
