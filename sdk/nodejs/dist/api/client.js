var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
import { GraphQLClient, gql } from "../index.js";
import { queryBuilder, queryFlatten } from "./utils.js";
class BaseClient {
    constructor({ queryTree, port } = {}) {
        this._queryTree = queryTree || [];
        this.port = port || 8080;
        this.client = new GraphQLClient(`http://localhost:${port}/query`);
    }
    get queryTree() {
        return this._queryTree;
    }
    _compute() {
        return __awaiter(this, void 0, void 0, function* () {
            // run the query and return the result.
            const query = queryBuilder(this._queryTree);
            const computeQuery = new Promise((resolve, reject) => __awaiter(this, void 0, void 0, function* () {
                const response = yield this.client.request(gql `${query}`).catch((error) => { reject(console.error(JSON.stringify(error, undefined, 2))); });
                resolve(queryFlatten(response));
            }));
            const result = yield computeQuery;
            return result;
        });
    }
}
export default class Client extends BaseClient {
    /**
     * Load a container from ID. Null ID returns an empty container (scratch).
     */
    container(args) {
        return new Container({ queryTree: [
                {
                    operation: 'container',
                    args
                }
            ], port: this.port });
    }
    /**
     * Construct a cache volume for a given cache key
     */
    cacheVolume(args) {
        return new CacheVolume({ queryTree: [
                {
                    operation: 'cacheVolume',
                    args
                }
            ], port: this.port });
    }
    /**
     * Query a git repository
     */
    git(args) {
        return new Git({ queryTree: [
                {
                    operation: 'git',
                    args
                }
            ], port: this.port });
    }
    /**
     * Query the host environment
     */
    host() {
        return new Host({ queryTree: [
                {
                    operation: 'host',
                }
            ], port: this.port });
    }
    secret(args) {
        return new Secret({ queryTree: [
                {
                    operation: 'secret',
                    args
                }
            ], port: this.port });
    }
}
class CacheVolume extends BaseClient {
    /**
     * A unique identifier for this container
     */
    id() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'id',
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
class Host extends BaseClient {
    envVariable(args) {
        return new HostVariable({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'envVariable',
                    args
                }
            ], port: this.port });
    }
    /**
     * The current working directory on the host
     */
    workdir(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'workdir',
                    args
                }
            ], port: this.port });
    }
}
class HostVariable extends BaseClient {
    secret() {
        return new Secret({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'secret'
                }
            ], port: this.port });
    }
    value() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'value'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
class Secret extends BaseClient {
    id() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'id'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    plaintext() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'plaintext'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
class Git extends BaseClient {
    /**
     * Details on one branch
     */
    branch(args) {
        return new Tree({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'branch',
                    args
                }
            ], port: this.port });
    }
}
class Tree extends BaseClient {
    /**
     * The filesystem tree at this ref
     */
    tree() {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'tree'
                }
            ], port: this.port });
    }
}
class File extends BaseClient {
    /**
   * The contents of the file
   */
    contents() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'contents'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The size of the file, in bytes
     */
    size() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'size'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
class Container extends BaseClient {
    /**
     * This container after executing the specified command inside it
     */
    exec(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'exec',
                    args
                }
            ], port: this.port });
    }
    /**
     * Initialize this container from the base image published at the given address
     */
    from(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'from',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container's root filesystem. Mounts are not included.
     */
    fs() {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'fs',
                }
            ], port: this.port });
    }
    /**
     * List of paths where a directory is mounted
     */
    mounts() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'mounts',
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Initialize this container from this DirectoryID
     */
    withFS(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withFS',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus a directory mounted at the given path
     */
    withMountedDirectory(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withMountedDirectory',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus a cache volume mounted at the given path
     */
    withMountedCache(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withMountedCache',
                    args
                }
            ], port: this.port });
    }
    /**
     * A unique identifier for this container
     */
    id() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'id',
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The output stream of the last executed command. Null if no command has been executed.
     */
    stdout() {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'stdout',
                }
            ], port: this.port });
    }
    /**
     * The error stream of the last executed command. Null if no command has been executed.
     */
    stderr() {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'stderr',
                }
            ], port: this.port });
    }
    /**
     * This container but with a different working directory
     */
    withWorkdir(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withWorkdir',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus an env variable containing the given secret
     * @arg name: string
     * @arg secret: string
     */
    withSecretVariable(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withSecretVariable',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus the given environment variable
     */
    withEnvVariable(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withEnvVariable',
                    args
                }
            ], port: this.port });
    }
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    directory(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'directory',
                    args
                }
            ], port: this.port });
    }
}
class Directory extends BaseClient {
    /**
     * Retrieve a file at the given path
     */
    file(args) {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'file',
                    args
                }
            ], port: this.port });
    }
    /**
     * Retrieve a directory at the given path. Mounts are included.
     */
    id() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'id'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Return a list of files and directories at the given path
     */
    entries(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'entries',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
