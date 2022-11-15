export type Maybe<T> = T | null;
export type Exact<T extends { [key: string]: unknown }> = { [K in keyof T]: T[K] };
export type MakeOptional<T, K extends keyof T> = Omit<T, K> & { [SubKey in K]?: Maybe<T[SubKey]> };
export type MakeMaybe<T, K extends keyof T> = Omit<T, K> & { [SubKey in K]: Maybe<T[SubKey]> };

/** All built-in and custom scalars, mapped to their actual values */
export type Scalars = {
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


export type CacheVolume = {
  __typename?: 'CacheVolume';
  id: Scalars['CacheID'];
};

export type Container = {
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


export type ContainerBuildArgs = {
  context: Scalars['DirectoryID'];
  dockerfile?: Maybe<Scalars['String']>;
};


export type ContainerDirectoryArgs = {
  path: Scalars['String'];
};


export type ContainerEnvVariableArgs = {
  name: Scalars['String'];
};


export type ContainerExecArgs = {
  args?: Maybe<Array<Scalars['String']>>;
  stdin?: Maybe<Scalars['String']>;
  redirectStdout?: Maybe<Scalars['String']>;
  redirectStderr?: Maybe<Scalars['String']>;
};


export type ContainerFileArgs = {
  path: Scalars['String'];
};


export type ContainerFromArgs = {
  address: Scalars['String'];
};


export type ContainerPublishArgs = {
  address: Scalars['String'];
};


export type ContainerWithDefaultArgsArgs = {
  args?: Maybe<Array<Scalars['String']>>;
};


export type ContainerWithEntrypointArgs = {
  args: Array<Scalars['String']>;
};


export type ContainerWithEnvVariableArgs = {
  name: Scalars['String'];
  value: Scalars['String'];
};


export type ContainerWithFsArgs = {
  id: Scalars['DirectoryID'];
};


export type ContainerWithMountedCacheArgs = {
  path: Scalars['String'];
  cache: Scalars['CacheID'];
  source?: Maybe<Scalars['DirectoryID']>;
};


export type ContainerWithMountedDirectoryArgs = {
  path: Scalars['String'];
  source: Scalars['DirectoryID'];
};


export type ContainerWithMountedFileArgs = {
  path: Scalars['String'];
  source: Scalars['FileID'];
};


export type ContainerWithMountedSecretArgs = {
  path: Scalars['String'];
  source: Scalars['SecretID'];
};


export type ContainerWithMountedTempArgs = {
  path: Scalars['String'];
};


export type ContainerWithSecretVariableArgs = {
  name: Scalars['String'];
  secret: Scalars['SecretID'];
};


export type ContainerWithUserArgs = {
  name: Scalars['String'];
};


export type ContainerWithWorkdirArgs = {
  path: Scalars['String'];
};


export type ContainerWithoutEnvVariableArgs = {
  name: Scalars['String'];
};


export type ContainerWithoutMountArgs = {
  path: Scalars['String'];
};



export type Directory = {
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


export type DirectoryDiffArgs = {
  other: Scalars['DirectoryID'];
};


export type DirectoryDirectoryArgs = {
  path: Scalars['String'];
};


export type DirectoryEntriesArgs = {
  path?: Maybe<Scalars['String']>;
};


export type DirectoryExportArgs = {
  path: Scalars['String'];
};


export type DirectoryFileArgs = {
  path: Scalars['String'];
};


export type DirectoryLoadProjectArgs = {
  configPath: Scalars['String'];
};


export type DirectoryWithCopiedFileArgs = {
  path: Scalars['String'];
  source: Scalars['FileID'];
};


export type DirectoryWithDirectoryArgs = {
  path: Scalars['String'];
  directory: Scalars['DirectoryID'];
  exclude?: Maybe<Array<Scalars['String']>>;
  include?: Maybe<Array<Scalars['String']>>;
};


export type DirectoryWithNewFileArgs = {
  path: Scalars['String'];
  contents?: Maybe<Scalars['String']>;
};


export type DirectoryWithoutDirectoryArgs = {
  path: Scalars['String'];
};


export type DirectoryWithoutFileArgs = {
  path: Scalars['String'];
};


export type EnvVariable = {
  __typename?: 'EnvVariable';
  name: Scalars['String'];
  value: Scalars['String'];
};

export type File = {
  __typename?: 'File';
  contents: Scalars['String'];
  id: Scalars['FileID'];
  secret: Secret;
  size: Scalars['Int'];
};


export type GitRef = {
  __typename?: 'GitRef';
  digest: Scalars['String'];
  tree: Directory;
};

export type GitRepository = {
  __typename?: 'GitRepository';
  branch: GitRef;
  branches: Array<Scalars['String']>;
  tag: GitRef;
  tags: Array<Scalars['String']>;
};


export type GitRepositoryBranchArgs = {
  name: Scalars['String'];
};


export type GitRepositoryTagArgs = {
  name: Scalars['String'];
};

export type Host = {
  __typename?: 'Host';
  directory: Directory;
  envVariable?: Maybe<HostVariable>;
  workdir: Directory;
};


export type HostDirectoryArgs = {
  exclude?: Maybe<Array<Scalars['String']>>;
  include?: Maybe<Array<Scalars['String']>>;
  path: Scalars['String'];
};


export type HostEnvVariableArgs = {
  name: Scalars['String'];
};


export type HostWorkdirArgs = {
  exclude?: Maybe<Array<Scalars['String']>>;
  include?: Maybe<Array<Scalars['String']>>;
};

export type HostVariable = {
  __typename?: 'HostVariable';
  secret: Secret;
  value: Scalars['String'];
};

export type Project = {
  __typename?: 'Project';
  extensions?: Maybe<Array<Project>>;
  generatedCode: Directory;
  install: Scalars['Boolean'];
  name: Scalars['String'];
  schema?: Maybe<Scalars['String']>;
  sdk?: Maybe<Scalars['String']>;
};

export type Query = {
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


export type QueryCacheVolumeArgs = {
  key: Scalars['String'];
};


export type QueryContainerArgs = {
  id?: Maybe<Scalars['ContainerID']>;
};


export type QueryDirectoryArgs = {
  id?: Maybe<Scalars['DirectoryID']>;
};


export type QueryFileArgs = {
  id: Scalars['FileID'];
};


export type QueryGitArgs = {
  url: Scalars['String'];
};


export type QueryHttpArgs = {
  url: Scalars['String'];
};


export type QueryProjectArgs = {
  name: Scalars['String'];
};


export type QuerySecretArgs = {
  id: Scalars['SecretID'];
};

export type Secret = {
  __typename?: 'Secret';
  id: Scalars['SecretID'];
  plaintext: Scalars['String'];
};



export type CacheId = any;


export type ContainerId = any;


export type DirectoryId = any;


export type FileId = any;


export type SecretId = any;


export type DateTime = any;
