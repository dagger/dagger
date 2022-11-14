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
/**
 * A directory whose contents persist across runs
 */
class CacheVolume extends BaseClient {
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
}
/**
 * An OCI-compatible container, also known as a docker container
 */
class Container extends BaseClient {
    /**
     * Initialize this container from a Dockerfile build
     */
    build(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'build',
                    args
                }
            ], port: this.port });
    }
    /**
     * Default arguments for future commands
     */
    defaultArgs() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'defaultArgs'
                }
            ];
            const response = yield this._compute();
            return response;
        });
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
    /**
     * Entrypoint to be prepended to the arguments of all commands
     */
    entrypoint() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'entrypoint'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The value of the specified environment variable
     */
    envVariable(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'envVariable',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * A list of environment variables passed to commands
     */
    envVariables() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'envVariables'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
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
     * Exit code of the last executed command. Zero means success.
     * Null if no command has been executed.
     */
    exitCode() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'exitCode'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Write the container as an OCI tarball to the destination file path on the host
     */
    export(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'export',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Retrieve a file at the given path. Mounts are included.
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
                    operation: 'fs'
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
                    operation: 'id'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * List of paths where a directory is mounted
     */
    mounts() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'mounts'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The platform this container executes and publishes as
     */
    platform() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'platform'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Publish this container as a new image, returning a fully qualified ref
     */
    publish(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'publish',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The error stream of the last executed command.
     * Null if no command has been executed.
     */
    stderr() {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'stderr'
                }
            ], port: this.port });
    }
    /**
     * The output stream of the last executed command.
     * Null if no command has been executed.
     */
    stdout() {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'stdout'
                }
            ], port: this.port });
    }
    /**
     * The user to be set for all commands
     */
    user() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'user'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Configures default arguments for future commands
     */
    withDefaultArgs(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withDefaultArgs',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container but with a different command entrypoint
     */
    withEntrypoint(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withEntrypoint',
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
     * This container plus a file mounted at the given path
     */
    withMountedFile(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withMountedFile',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus a secret mounted into a file at the given path
     */
    withMountedSecret(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withMountedSecret',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus a temporary directory mounted at the given path
     */
    withMountedTemp(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withMountedTemp',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container plus an env variable containing the given secret
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
     * This container but with a different command user
     */
    withUser(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withUser',
                    args
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
     * This container minus the given environment variable
     */
    withoutEnvVariable(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withoutEnvVariable',
                    args
                }
            ], port: this.port });
    }
    /**
     * This container after unmounting everything at the given path.
     */
    withoutMount(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withoutMount',
                    args
                }
            ], port: this.port });
    }
    /**
     * The working directory for all commands
     */
    workdir() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'workdir'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
/**
 * A directory
 */
class Directory extends BaseClient {
    /**
     * The difference between this directory and an another directory
     */
    diff(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'diff',
                    args
                }
            ], port: this.port });
    }
    /**
     * Retrieve a directory at the given path
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
    /**
     * Write the contents of the directory to a path on the host
     */
    export(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'export',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
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
     * The content-addressed identifier of the directory
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
     * load a project's metadata
     */
    loadProject(args) {
        return new Project({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'loadProject',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory plus a directory written at the given path
     */
    withDirectory(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withDirectory',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory plus the contents of the given file copied to the given path
     */
    withFile(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withFile',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory plus a new directory created at the given path
     */
    withNewDirectory(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withNewDirectory',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory plus a new file written at the given path
     */
    withNewFile(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withNewFile',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory with the directory at the given path removed
     */
    withoutDirectory(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withoutDirectory',
                    args
                }
            ], port: this.port });
    }
    /**
     * This directory with the file at the given path removed
     */
    withoutFile(args) {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'withoutFile',
                    args
                }
            ], port: this.port });
    }
}
/**
 * EnvVariable is a simple key value object that represents an environment variable.
 */
class EnvVariable extends BaseClient {
    /**
     * name is the environment variable name.
     */
    name() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'name'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * value is the environment variable value
     */
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
/**
 * A file
 */
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
     * Write the file to a file path on the host
     */
    export(args) {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'export',
                    args
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * The content-addressed identifier of the file
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
    secret() {
        return new Secret({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'secret'
                }
            ], port: this.port });
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
/**
 * A git ref (tag or branch)
 */
class GitRef extends BaseClient {
    /**
     * The digest of the current value of this ref
     */
    digest() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'digest'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
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
/**
 * A git repository
 */
class GitRepository extends BaseClient {
    /**
     * Details on one branch
     */
    branch(args) {
        return new GitRef({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'branch',
                    args
                }
            ], port: this.port });
    }
    /**
     * List of branches on the repository
     */
    branches() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'branches'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Details on one commit
     */
    commit(args) {
        return new GitRef({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'commit',
                    args
                }
            ], port: this.port });
    }
    /**
     * Details on one tag
     */
    tag(args) {
        return new GitRef({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'tag',
                    args
                }
            ], port: this.port });
    }
    /**
     * List of tags on the repository
     */
    tags() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'tags'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
/**
 * Information about the host execution environment
 */
class Host extends BaseClient {
    /**
     * Access a directory on the host
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
    /**
     * Lookup the value of an environment variable. Null if the variable is not available.
     */
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
/**
 * An environment variable on the host environment
 */
class HostVariable extends BaseClient {
    /**
     * A secret referencing the value of this variable
     */
    secret() {
        return new Secret({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'secret'
                }
            ], port: this.port });
    }
    /**
     * The value of this variable
     */
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
/**
 * A set of scripts and/or extensions
 */
class Project extends BaseClient {
    /**
     * extensions in this project
     */
    extensions() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'extensions'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Code files generated by the SDKs in the project
     */
    generatedCode() {
        return new Directory({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'generatedCode'
                }
            ], port: this.port });
    }
    /**
     * install the project's schema
     */
    install() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'install'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * name of the project
     */
    name() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'name'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * schema provided by the project
     */
    schema() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'schema'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * sdk used to generate code for and/or execute this project
     */
    sdk() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'sdk'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
}
export default class Client extends BaseClient {
    /**
     * Construct a cache volume for a given cache key
     */
    cacheVolume(args) {
        return new CacheVolume({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'cacheVolume',
                    args
                }
            ], port: this.port });
    }
    /**
     * Load a container from ID.
     * Null ID returns an empty container (scratch).
     * Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.
     */
    container(args) {
        return new Container({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'container',
                    args
                }
            ], port: this.port });
    }
    /**
     * The default platform of the builder.
     */
    defaultPlatform() {
        return __awaiter(this, void 0, void 0, function* () {
            this._queryTree = [
                ...this._queryTree,
                {
                    operation: 'defaultPlatform'
                }
            ];
            const response = yield this._compute();
            return response;
        });
    }
    /**
     * Load a directory by ID. No argument produces an empty directory.
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
    /**
     * Load a file by ID
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
     * Query a git repository
     */
    git(args) {
        return new GitRepository({ queryTree: [
                ...this._queryTree,
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
                ...this._queryTree,
                {
                    operation: 'host'
                }
            ], port: this.port });
    }
    /**
     * An http remote
     */
    http(args) {
        return new File({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'http',
                    args
                }
            ], port: this.port });
    }
    /**
     * Look up a project by name
     */
    project(args) {
        return new Project({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'project',
                    args
                }
            ], port: this.port });
    }
    /**
     * Load a secret from its ID
     */
    secret(args) {
        return new Secret({ queryTree: [
                ...this._queryTree,
                {
                    operation: 'secret',
                    args
                }
            ], port: this.port });
    }
}
/**
 * A reference to a secret value, which can be handled more safely than the value itself
 */
class Secret extends BaseClient {
    /**
     * The identifier for this secret
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
     * The value of this secret
     */
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
