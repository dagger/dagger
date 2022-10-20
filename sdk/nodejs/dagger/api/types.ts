// THIS FILE HAS BEEN GENERATED WITH GET-GRAPHQL-SCHEMA

export type Maybe<T> = T | null;
export type InputMaybe<T> = Maybe<T>;
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
  /** A global cache volume identifier */
  CacheID: any;
  /**
   * The address (also known as "ref") of a container published as an OCI image.
   *
   * Examples:
   *         - "alpine"
   *         - "index.docker.io/alpine"
   *         - "index.docker.io/alpine:latest"
   *         - "index.docker.io/alpine:latest@sha256deadbeefdeadbeefdeadbeef"
   */
  ContainerAddress: any;
  /** A unique container identifier. Null designates an empty container (scratch). */
  ContainerID: any;
  /** The `DateTime` scalar type represents a DateTime. The DateTime is serialized as an RFC 3339 quoted string */
  DateTime: any;
  /** A content-addressed directory identifier */
  DirectoryID: any;
  FileID: any;
  /** An identifier for a directory on the host */
  HostDirectoryID: any;
  /** A unique identifier for a secret */
  SecretID: any;
};

/** A directory whose contents persist across runs */
export type CacheVolume = {
  __typename?: 'CacheVolume';
  id: Scalars['CacheID'];
};

/** An OCI-compatible container, also known as a docker container */
export type Container = {
  __typename?: 'Container';
  /** Default arguments for future commands */
  defaultArgs?: Maybe<Array<Scalars['String']>>;
  /** Retrieve a directory at the given path. Mounts are included. */
  directory: Directory;
  /** Entrypoint to be prepended to the arguments of all commands */
  entrypoint?: Maybe<Array<Scalars['String']>>;
  /** The value of the specified environment variable */
  envVariable?: Maybe<Scalars['String']>;
  /** A list of environment variables passed to commands */
  envVariables: Array<EnvVariable>;
  /** This container after executing the specified command inside it */
  exec: Container;
  /**
   * Exit code of the last executed command. Zero means success.
   * Null if no command has been executed.
   */
  exitCode?: Maybe<Scalars['Int']>;
  /** Retrieve a file at the given path. Mounts are included. */
  file: File;
  /** Initialize this container from the base image published at the given address */
  from: Container;
  /** This container's root filesystem. Mounts are not included. */
  fs: Directory;
  /** A unique identifier for this container */
  id: Scalars['ContainerID'];
  /** List of paths where a directory is mounted */
  mounts: Array<Scalars['String']>;
  /** Publish this container as a new image */
  publish: Scalars['ContainerAddress'];
  /**
   * The error stream of the last executed command.
   * Null if no command has been executed.
   */
  stderr?: Maybe<File>;
  /**
   * The output stream of the last executed command.
   * Null if no command has been executed.
   */
  stdout?: Maybe<File>;
  /** The user to be set for all commands */
  user?: Maybe<Scalars['String']>;
  /** Configures default arguments for future commands */
  withDefaultArgs: Container;
  /** This container but with a different command entrypoint */
  withEntrypoint: Container;
  /** This container plus the given environment variable */
  withEnvVariable: Container;
  /** Initialize this container from this DirectoryID */
  withFS: Container;
  /** This container plus a cache volume mounted at the given path */
  withMountedCache: Container;
  /** This container plus a directory mounted at the given path */
  withMountedDirectory: Container;
  /** This container plus a file mounted at the given path */
  withMountedFile: Container;
  /** This container plus a secret mounted into a file at the given path */
  withMountedSecret: Container;
  /** This container plus a temporary directory mounted at the given path */
  withMountedTemp: Container;
  /** This container plus an env variable containing the given secret */
  withSecretVariable: Container;
  /** This container but with a different command user */
  withUser: Container;
  /** This container but with a different working directory */
  withWorkdir: Container;
  /** This container minus the given environment variable */
  withoutEnvVariable: Container;
  /** This container after unmounting everything at the given path. */
  withoutMount: Container;
  /** The working directory for all commands */
  workdir?: Maybe<Scalars['String']>;
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerDirectoryArgs = {
  path: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerEnvVariableArgs = {
  name: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerExecArgs = {
  args: InputMaybe<Array<Scalars['String']>>;
  stdin?: InputMaybe<Scalars['String']>;
  redirectStdout?: InputMaybe<Scalars['String']>;
  redirectStderr?: InputMaybe<Scalars['String']>;
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerFileArgs = {
  path: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerFromArgs = {
  address: Scalars['ContainerAddress'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerPublishArgs = {
  address: Scalars['ContainerAddress'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithDefaultArgsArgs = {
  args?: InputMaybe<Array<Scalars['String']>>;
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithEntrypointArgs = {
  args: Array<Scalars['String']>;
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithEnvVariableArgs = {
  name: Scalars['String'];
  value: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithFsArgs = {
  id: Scalars['DirectoryID'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithMountedCacheArgs = {
  path: Scalars['String'];
  cache: Scalars['CacheID'];
  source?: InputMaybe<Scalars['DirectoryID']>;
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithMountedDirectoryArgs = {
  path: Scalars['String'];
  source: Scalars['DirectoryID'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithMountedFileArgs = {
  path: Scalars['String'];
  source: Scalars['FileID'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithMountedSecretArgs = {
  path: Scalars['String'];
  source: Scalars['SecretID'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithMountedTempArgs = {
  path: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithSecretVariableArgs = {
  secret: Scalars['SecretID'];
  name: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithUserArgs = {
  name: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithWorkdirArgs = {
  path: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithoutEnvVariableArgs = {
  name: Scalars['String'];
};


/** An OCI-compatible container, also known as a docker container */
export type ContainerWithoutMountArgs = {
  path: Scalars['String'];
};

/** A directory */
export type Directory = {
  __typename?: 'Directory';
  /** Return a list of files and directories at the given path */
  contents: Array<Scalars['String']>;
  /** The difference between this directory and an another directory */
  diff: Directory;
  /** Retrieve a directory at the given path */
  directory: Directory;
  /** Retrieve a file at the given path */
  file: File;
  /** The content-addressed identifier of the directory */
  id: Scalars['DirectoryID'];
  /** load a project's metadata */
  loadProject: Project;
  /** This directory plus the contents of the given file copied to the given path */
  withCopiedFile: Directory;
  /** This directory plus a directory written at the given path */
  withDirectory: Directory;
  /** This directory plus a new file written at the given path */
  withNewFile: Directory;
  /** This directory with the directory at the given path removed */
  withoutDirectory: Directory;
  /** This directory with the file at the given path removed */
  withoutFile: Directory;
};


/** A directory */
export type DirectoryContentsArgs = {
  path?: InputMaybe<Scalars['String']>;
};


/** A directory */
export type DirectoryDiffArgs = {
  other: Scalars['DirectoryID'];
};


/** A directory */
export type DirectoryDirectoryArgs = {
  path: Scalars['String'];
};


/** A directory */
export type DirectoryFileArgs = {
  path: Scalars['String'];
};


/** A directory */
export type DirectoryLoadProjectArgs = {
  configPath: Scalars['String'];
};


/** A directory */
export type DirectoryWithCopiedFileArgs = {
  path: Scalars['String'];
  source: Scalars['FileID'];
};


/** A directory */
export type DirectoryWithDirectoryArgs = {
  path: Scalars['String'];
  directory: Scalars['DirectoryID'];
};


/** A directory */
export type DirectoryWithNewFileArgs = {
  path: Scalars['String'];
  contents?: InputMaybe<Scalars['String']>;
};


/** A directory */
export type DirectoryWithoutDirectoryArgs = {
  path: Scalars['String'];
};


/** A directory */
export type DirectoryWithoutFileArgs = {
  path: Scalars['String'];
};

/** EnvVariable is a simple key value object that represents an environment variable. */
export type EnvVariable = {
  __typename?: 'EnvVariable';
  /** name is the environment variable name. */
  name: Scalars['String'];
  /** value is the environment variable value */
  value: Scalars['String'];
};

/** A file */
export type File = {
  __typename?: 'File';
  /** The contents of the file */
  contents: Scalars['String'];
  /** The content-addressed identifier of the file */
  id: Scalars['FileID'];
  secret: Secret;
  /** The size of the file, in bytes */
  size: Scalars['Int'];
};

/** A git ref (tag or branch) */
export type GitRef = {
  __typename?: 'GitRef';
  /** The digest of the current value of this ref */
  digest: Scalars['String'];
  /** The filesystem tree at this ref */
  tree: Directory;
};

/** A git repository */
export type GitRepository = {
  __typename?: 'GitRepository';
  /** Details on one branch */
  branch: GitRef;
  /** List of branches on the repository */
  branches: Array<Scalars['String']>;
  /** Details on one tag */
  tag: GitRef;
  /** List of tags on the repository */
  tags: Array<Scalars['String']>;
};


/** A git repository */
export type GitRepositoryBranchArgs = {
  name: Scalars['String'];
};


/** A git repository */
export type GitRepositoryTagArgs = {
  name: Scalars['String'];
};

/** Information about the host execution environment */
export type Host = {
  __typename?: 'Host';
  /** Access a directory on the host */
  directory: HostDirectory;
  /** Lookup the value of an environment variable. Null if the variable is not available. */
  variable?: Maybe<HostVariable>;
  /** The current working directory on the host */
  workdir: HostDirectory;
};


/** Information about the host execution environment */
export type HostDirectoryArgs = {
  id: Scalars['HostDirectoryID'];
};


/** Information about the host execution environment */
export type HostVariableArgs = {
  name: Scalars['String'];
};

/** A directory on the host */
export type HostDirectory = {
  __typename?: 'HostDirectory';
  /** Read the contents of the directory */
  read: Directory;
  /** Write the contents of another directory to the directory */
  write: Scalars['Boolean'];
};


/** A directory on the host */
export type HostDirectoryWriteArgs = {
  contents: Scalars['DirectoryID'];
  path?: InputMaybe<Scalars['String']>;
};

/** An environment variable on the host environment */
export type HostVariable = {
  __typename?: 'HostVariable';
  /** A secret referencing the value of this variable */
  secret: Secret;
  /** The value of this variable */
  value: Scalars['String'];
};

/** A set of scripts and/or extensions */
export type Project = {
  __typename?: 'Project';
  /** extensions in this project */
  extensions?: Maybe<Array<Project>>;
  /** Code files generated by the SDKs in the project */
  generatedCode: Directory;
  /** install the project's schema */
  install: Scalars['Boolean'];
  /** name of the project */
  name: Scalars['String'];
  /** schema provided by the project */
  schema?: Maybe<Scalars['String']>;
  /** sdk used to generate code for and/or execute this project */
  sdk?: Maybe<Scalars['String']>;
};

export type Query = {
  __typename?: 'Query';
  /** Construct a cache volume for a given cache key */
  cacheVolume: CacheVolume;
  /**
   * Load a container from ID.
   * Null ID returns an empty container (scratch).
   */
  container: Container;
  /** Load a directory by ID. No argument produces an empty directory. */
  directory: Directory;
  /** Load a file by ID */
  file?: Maybe<File>;
  /** Query a git repository */
  git: GitRepository;
  /** Query the host environment */
  host: Host;
  /** An http remote */
  http: File;
  /** Look up a project by name */
  project: Project;
  /** Load a secret from its ID */
  secret: Secret;
};


export type QueryCacheVolumeArgs = {
  key: Scalars['String'];
};


export type QueryContainerArgs = {
  id?: InputMaybe<Scalars['ContainerID']>;
};


export type QueryDirectoryArgs = {
  id?: InputMaybe<Scalars['DirectoryID']>;
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

/** A reference to a secret value, which can be handled more safely than the value itself */
export type Secret = {
  __typename?: 'Secret';
  /** The identifier for this secret */
  id: Scalars['SecretID'];
  /** The value of this secret */
  plaintext: Scalars['String'];
};
