export declare type Maybe<T> = T | null;
export declare type Exact<T extends {
    [key: string]: unknown;
}> = {
    [K in keyof T]: T[K];
};
export declare type MakeOptional<T, K extends keyof T> = Omit<T, K> & {
    [SubKey in K]?: Maybe<T[SubKey]>;
};
export declare type MakeMaybe<T, K extends keyof T> = Omit<T, K> & {
    [SubKey in K]: Maybe<T[SubKey]>;
};
/** All built-in and custom scalars, mapped to their actual values */
export declare type Scalars = {
    ID: string;
    String: string;
    Boolean: boolean;
    Int: number;
    Float: number;
    CacheID: any;
    ContainerID: any;
    DirectoryID: any;
    FileID: any;
    SecretID: any;
    DateTime: any;
};
export declare type CacheVolume = {
    __typename?: 'CacheVolume';
    id: Scalars['CacheID'];
};
export declare type Container = {
    __typename?: 'Container';
    build: Container;
    defaultArgs?: Maybe<Array<Scalars['String']>>;
    directory: Directory;
    entrypoint?: Maybe<Array<Scalars['String']>>;
    envVariable?: Maybe<Scalars['String']>;
    envVariables: Array<EnvVariable>;
    exec: Container;
    exitCode?: Maybe<Scalars['Int']>;
    file: File;
    from: Container;
    fs: Directory;
    id: Scalars['ContainerID'];
    mounts: Array<Scalars['String']>;
    publish: Scalars['String'];
    stderr?: Maybe<File>;
    stdout?: Maybe<File>;
    user?: Maybe<Scalars['String']>;
    withDefaultArgs: Container;
    withEntrypoint: Container;
    withEnvVariable: Container;
    withFS: Container;
    withMountedCache: Container;
    withMountedDirectory: Container;
    withMountedFile: Container;
    withMountedSecret: Container;
    withMountedTemp: Container;
    withSecretVariable: Container;
    withUser: Container;
    withWorkdir: Container;
    withoutEnvVariable: Container;
    withoutMount: Container;
    workdir?: Maybe<Scalars['String']>;
};
export declare type ContainerBuildArgs = {
    context: Scalars['DirectoryID'];
    dockerfile?: Maybe<Scalars['String']>;
};
export declare type ContainerDirectoryArgs = {
    path: Scalars['String'];
};
export declare type ContainerEnvVariableArgs = {
    name: Scalars['String'];
};
export declare type ContainerExecArgs = {
    args?: Maybe<Array<Scalars['String']>>;
    stdin?: Maybe<Scalars['String']>;
    redirectStdout?: Maybe<Scalars['String']>;
    redirectStderr?: Maybe<Scalars['String']>;
};
export declare type ContainerFileArgs = {
    path: Scalars['String'];
};
export declare type ContainerFromArgs = {
    address: Scalars['String'];
};
export declare type ContainerPublishArgs = {
    address: Scalars['String'];
};
export declare type ContainerWithDefaultArgsArgs = {
    args?: Maybe<Array<Scalars['String']>>;
};
export declare type ContainerWithEntrypointArgs = {
    args: Array<Scalars['String']>;
};
export declare type ContainerWithEnvVariableArgs = {
    name: Scalars['String'];
    value: Scalars['String'];
};
export declare type ContainerWithFsArgs = {
    id: Scalars['DirectoryID'];
};
export declare type ContainerWithMountedCacheArgs = {
    path: Scalars['String'];
    cache: Scalars['CacheID'];
    source?: Maybe<Scalars['DirectoryID']>;
};
export declare type ContainerWithMountedDirectoryArgs = {
    path: Scalars['String'];
    source: Scalars['DirectoryID'];
};
export declare type ContainerWithMountedFileArgs = {
    path: Scalars['String'];
    source: Scalars['FileID'];
};
export declare type ContainerWithMountedSecretArgs = {
    path: Scalars['String'];
    source: Scalars['SecretID'];
};
export declare type ContainerWithMountedTempArgs = {
    path: Scalars['String'];
};
export declare type ContainerWithSecretVariableArgs = {
    name: Scalars['String'];
    secret: Scalars['SecretID'];
};
export declare type ContainerWithUserArgs = {
    name: Scalars['String'];
};
export declare type ContainerWithWorkdirArgs = {
    path: Scalars['String'];
};
export declare type ContainerWithoutEnvVariableArgs = {
    name: Scalars['String'];
};
export declare type ContainerWithoutMountArgs = {
    path: Scalars['String'];
};
export declare type Directory = {
    __typename?: 'Directory';
    diff: Directory;
    directory: Directory;
    entries: Array<Scalars['String']>;
    export: Scalars['Boolean'];
    file: File;
    id: Scalars['DirectoryID'];
    loadProject: Project;
    withCopiedFile: Directory;
    withDirectory: Directory;
    withNewFile: Directory;
    withoutDirectory: Directory;
    withoutFile: Directory;
};
export declare type DirectoryDiffArgs = {
    other: Scalars['DirectoryID'];
};
export declare type DirectoryDirectoryArgs = {
    path: Scalars['String'];
};
export declare type DirectoryEntriesArgs = {
    path?: Maybe<Scalars['String']>;
};
export declare type DirectoryExportArgs = {
    path: Scalars['String'];
};
export declare type DirectoryFileArgs = {
    path: Scalars['String'];
};
export declare type DirectoryLoadProjectArgs = {
    configPath: Scalars['String'];
};
export declare type DirectoryWithCopiedFileArgs = {
    path: Scalars['String'];
    source: Scalars['FileID'];
};
export declare type DirectoryWithDirectoryArgs = {
    path: Scalars['String'];
    directory: Scalars['DirectoryID'];
    exclude?: Maybe<Array<Scalars['String']>>;
    include?: Maybe<Array<Scalars['String']>>;
};
export declare type DirectoryWithNewFileArgs = {
    path: Scalars['String'];
    contents?: Maybe<Scalars['String']>;
};
export declare type DirectoryWithoutDirectoryArgs = {
    path: Scalars['String'];
};
export declare type DirectoryWithoutFileArgs = {
    path: Scalars['String'];
};
export declare type EnvVariable = {
    __typename?: 'EnvVariable';
    name: Scalars['String'];
    value: Scalars['String'];
};
export declare type File = {
    __typename?: 'File';
    contents: Scalars['String'];
    id: Scalars['FileID'];
    secret: Secret;
    size: Scalars['Int'];
};
export declare type GitRef = {
    __typename?: 'GitRef';
    digest: Scalars['String'];
    tree: Directory;
};
export declare type GitRepository = {
    __typename?: 'GitRepository';
    branch: GitRef;
    branches: Array<Scalars['String']>;
    tag: GitRef;
    tags: Array<Scalars['String']>;
};
export declare type GitRepositoryBranchArgs = {
    name: Scalars['String'];
};
export declare type GitRepositoryTagArgs = {
    name: Scalars['String'];
};
export declare type Host = {
    __typename?: 'Host';
    directory: Directory;
    envVariable?: Maybe<HostVariable>;
    workdir: Directory;
};
export declare type HostDirectoryArgs = {
    exclude?: Maybe<Array<Scalars['String']>>;
    include?: Maybe<Array<Scalars['String']>>;
    path: Scalars['String'];
};
export declare type HostEnvVariableArgs = {
    name: Scalars['String'];
};
export declare type HostWorkdirArgs = {
    exclude?: Maybe<Array<Scalars['String']>>;
    include?: Maybe<Array<Scalars['String']>>;
};
export declare type HostVariable = {
    __typename?: 'HostVariable';
    secret: Secret;
    value: Scalars['String'];
};
export declare type Project = {
    __typename?: 'Project';
    extensions?: Maybe<Array<Project>>;
    generatedCode: Directory;
    install: Scalars['Boolean'];
    name: Scalars['String'];
    schema?: Maybe<Scalars['String']>;
    sdk?: Maybe<Scalars['String']>;
};
export declare type Query = {
    __typename?: 'Query';
    cacheVolume: CacheVolume;
    container: Container;
    directory: Directory;
    file?: Maybe<File>;
    git: GitRepository;
    host: Host;
    http: File;
    project: Project;
    secret: Secret;
};
export declare type QueryCacheVolumeArgs = {
    key: Scalars['String'];
};
export declare type QueryContainerArgs = {
    id?: Maybe<Scalars['ContainerID']>;
};
export declare type QueryDirectoryArgs = {
    id?: Maybe<Scalars['DirectoryID']>;
};
export declare type QueryFileArgs = {
    id: Scalars['FileID'];
};
export declare type QueryGitArgs = {
    url: Scalars['String'];
};
export declare type QueryHttpArgs = {
    url: Scalars['String'];
};
export declare type QueryProjectArgs = {
    name: Scalars['String'];
};
export declare type QuerySecretArgs = {
    id: Scalars['SecretID'];
};
export declare type Secret = {
    __typename?: 'Secret';
    id: Scalars['SecretID'];
    plaintext: Scalars['String'];
};
export declare type CacheId = any;
export declare type ContainerId = any;
export declare type DirectoryId = any;
export declare type FileId = any;
export declare type SecretId = any;
export declare type DateTime = any;
//# sourceMappingURL=types.d.ts.map