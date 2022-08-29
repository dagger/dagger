import { FSID, SecretID } from '@dagger.io/dagger'
import { GraphQLClient } from 'graphql-request';
import * as Dom from 'graphql-request/dist/types.dom';
import gql from 'graphql-tag';
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
  FSID: FSID;
  SecretID: SecretID;
};

export type CacheMountInput = {
  /** Cache mount name */
  name: Scalars['String'];
  /** path at which the cache will be mounted */
  path: Scalars['String'];
  /** Cache mount sharing mode (TODO: switch to enum) */
  sharingMode: Scalars['String'];
};

/** Core API */
export type Core = {
  __typename?: 'Core';
  /** Add a secret */
  addSecret: Scalars['SecretID'];
  /** Fetch a client directory */
  clientdir: Filesystem;
  /** Look up an extension by name */
  extension: Extension;
  /** Look up a filesystem by its ID */
  filesystem: Filesystem;
  /** Fetch a git repository */
  git: Filesystem;
  /** Fetch an OCI image */
  image: Filesystem;
  /** Look up a secret by ID */
  secret: Scalars['String'];
};


/** Core API */
export type CoreAddSecretArgs = {
  plaintext: Scalars['String'];
};


/** Core API */
export type CoreClientdirArgs = {
  id: Scalars['String'];
};


/** Core API */
export type CoreExtensionArgs = {
  name: Scalars['String'];
};


/** Core API */
export type CoreFilesystemArgs = {
  id: Scalars['FSID'];
};


/** Core API */
export type CoreGitArgs = {
  ref?: InputMaybe<Scalars['String']>;
  remote: Scalars['String'];
};


/** Core API */
export type CoreImageArgs = {
  ref: Scalars['String'];
};


/** Core API */
export type CoreSecretArgs = {
  id: Scalars['SecretID'];
};

/** Command execution */
export type Exec = {
  __typename?: 'Exec';
  /** Exit code of the command */
  exitCode?: Maybe<Scalars['Int']>;
  /** Modified filesystem */
  fs: Filesystem;
  /** Modified mounted filesystem */
  mount: Filesystem;
  /** stderr of the command */
  stderr?: Maybe<Scalars['String']>;
  /** stdout of the command */
  stdout?: Maybe<Scalars['String']>;
};


/** Command execution */
export type ExecMountArgs = {
  path: Scalars['String'];
};


/** Command execution */
export type ExecStderrArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};


/** Command execution */
export type ExecStdoutArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};

export type ExecEnvInput = {
  /** Env var name */
  name: Scalars['String'];
  /** Env var value */
  value: Scalars['String'];
};

export type ExecInput = {
  /**
   * Command to execute
   * Example: ["echo", "hello, world!"]
   */
  args: Array<Scalars['String']>;
  /** Cached mounts */
  cacheMounts?: InputMaybe<Array<CacheMountInput>>;
  /** Env vars */
  env?: InputMaybe<Array<ExecEnvInput>>;
  /** Filesystem mounts */
  mounts?: InputMaybe<Array<MountInput>>;
  /** Secret env vars */
  secretEnv?: InputMaybe<Array<ExecSecretEnvInput>>;
  /** Working directory */
  workdir?: InputMaybe<Scalars['String']>;
};

export type ExecSecretEnvInput = {
  /** Secret env var value */
  id: Scalars['SecretID'];
  /** Env var name */
  name: Scalars['String'];
};

/** Extension representation */
export type Extension = {
  __typename?: 'Extension';
  /** dependencies for this extension */
  dependencies?: Maybe<Array<Extension>>;
  /** install the extension, stitching its schema into the API */
  install: Scalars['Boolean'];
  /** name of the extension */
  name: Scalars['String'];
  /** operations for this extension */
  operations?: Maybe<Scalars['String']>;
  /** schema of the extension */
  schema?: Maybe<Scalars['String']>;
};

/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type Filesystem = {
  __typename?: 'Filesystem';
  /** docker build using this filesystem as context */
  dockerbuild: Filesystem;
  /** execute a command inside this filesystem */
  exec: Exec;
  /** read a file at path */
  file?: Maybe<Scalars['String']>;
  id: Scalars['FSID'];
  /** load an extension's metadata */
  loadExtension: Extension;
  yarn: Filesystem;
};


/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type FilesystemDockerbuildArgs = {
  dockerfile?: InputMaybe<Scalars['String']>;
};


/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type FilesystemExecArgs = {
  input: ExecInput;
};


/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type FilesystemFileArgs = {
  lines?: InputMaybe<Scalars['Int']>;
  path: Scalars['String'];
};


/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type FilesystemLoadExtensionArgs = {
  configPath: Scalars['String'];
};


/**
 * A reference to a filesystem tree.
 *
 * For example:
 *  - The root filesystem of a container
 *  - A source code repository
 *  - A directory containing binary artifacts
 *
 * Rule of thumb: if it fits in a tar archive, it fits in a Filesystem.
 */
export type FilesystemYarnArgs = {
  runArgs?: InputMaybe<Array<Scalars['String']>>;
};

export type MountInput = {
  /** filesystem to mount */
  fs: Scalars['FSID'];
  /** path at which the filesystem will be mounted */
  path: Scalars['String'];
};

export type Query = {
  __typename?: 'Query';
  /** Core API */
  core: Core;
  yarn: Yarn;
};

export type Yarn = {
  __typename?: 'Yarn';
  script: Filesystem;
};


export type YarnScriptArgs = {
  runArgs?: InputMaybe<Array<Scalars['String']>>;
  source: Scalars['FSID'];
};

export type ScriptQueryVariables = Exact<{
  source: Scalars['FSID'];
  runArgs?: InputMaybe<Array<Scalars['String']> | Scalars['String']>;
}>;


export type ScriptQuery = { __typename?: 'Query', yarn: { __typename?: 'Yarn', script: { __typename?: 'Filesystem', id: FSID } } };


export const ScriptDocument = gql`
    query Script($source: FSID!, $runArgs: [String!]) {
  yarn {
    script(source: $source, runArgs: $runArgs) {
      id
    }
  }
}
    `;

export type SdkFunctionWrapper = <T>(action: (requestHeaders?:Record<string, string>) => Promise<T>, operationName: string, operationType?: string) => Promise<T>;


const defaultWrapper: SdkFunctionWrapper = (action, _operationName, _operationType) => action();

export function getSdk(client: GraphQLClient, withWrapper: SdkFunctionWrapper = defaultWrapper) {
  return {
    Script(variables: ScriptQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ScriptQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<ScriptQuery>(ScriptDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Script', 'query');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;