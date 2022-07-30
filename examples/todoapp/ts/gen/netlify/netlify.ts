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

export type Core = {
  __typename?: 'Core';
  clientdir: Filesystem;
  filesystem: Filesystem;
  git: Filesystem;
  image: Filesystem;
  secret: Scalars['String'];
};


export type CoreClientdirArgs = {
  id: Scalars['String'];
};


export type CoreFilesystemArgs = {
  id: Scalars['FSID'];
};


export type CoreGitArgs = {
  ref?: InputMaybe<Scalars['String']>;
  remote: Scalars['String'];
};


export type CoreImageArgs = {
  ref: Scalars['String'];
};


export type CoreSecretArgs = {
  id: Scalars['SecretID'];
};

export type Deploy = {
  __typename?: 'Deploy';
  deployUrl: Scalars['String'];
  logsUrl?: Maybe<Scalars['String']>;
  url: Scalars['String'];
};

export type Exec = {
  __typename?: 'Exec';
  exitCode?: Maybe<Scalars['Int']>;
  fs: Filesystem;
  mount: Filesystem;
  stderr?: Maybe<Scalars['String']>;
  stdout?: Maybe<Scalars['String']>;
};


export type ExecMountArgs = {
  path: Scalars['String'];
};


export type ExecStderrArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};


export type ExecStdoutArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};

export type ExecInput = {
  args: Array<Scalars['String']>;
  mounts?: InputMaybe<Array<MountInput>>;
  workdir?: InputMaybe<Scalars['String']>;
};

export type Filesystem = {
  __typename?: 'Filesystem';
  dockerbuild: Filesystem;
  exec: Exec;
  file?: Maybe<Scalars['String']>;
  id: Scalars['FSID'];
};


export type FilesystemDockerbuildArgs = {
  dockerfile?: InputMaybe<Scalars['String']>;
};


export type FilesystemExecArgs = {
  input: ExecInput;
};


export type FilesystemFileArgs = {
  lines?: InputMaybe<Scalars['Int']>;
  path: Scalars['String'];
};

export type MountInput = {
  fs: Scalars['FSID'];
  path: Scalars['String'];
};

export type Netlify = {
  __typename?: 'Netlify';
  deploy: Deploy;
};


export type NetlifyDeployArgs = {
  contents: Scalars['FSID'];
  siteName?: InputMaybe<Scalars['String']>;
  subdir?: InputMaybe<Scalars['String']>;
  token: Scalars['SecretID'];
};

export type Package = {
  __typename?: 'Package';
  fs?: Maybe<Filesystem>;
  name: Scalars['String'];
  operations: Scalars['String'];
  schema: Scalars['String'];
};

export type Query = {
  __typename?: 'Query';
  netlify: Netlify;
};

export type DeployQueryVariables = Exact<{
  contents: Scalars['FSID'];
  subdir?: InputMaybe<Scalars['String']>;
  siteName?: InputMaybe<Scalars['String']>;
  token: Scalars['SecretID'];
}>;


export type DeployQuery = { __typename?: 'Query', netlify: { __typename?: 'Netlify', deploy: { __typename?: 'Deploy', url: string, deployUrl: string } } };


export const DeployDocument = gql`
    query Deploy($contents: FSID!, $subdir: String, $siteName: String, $token: SecretID!) {
  netlify {
    deploy(contents: $contents, subdir: $subdir, siteName: $siteName, token: $token) {
      url
      deployUrl
    }
  }
}
    `;

export type SdkFunctionWrapper = <T>(action: (requestHeaders?:Record<string, string>) => Promise<T>, operationName: string, operationType?: string) => Promise<T>;


const defaultWrapper: SdkFunctionWrapper = (action, _operationName, _operationType) => action();

export function getSdk(client: GraphQLClient, withWrapper: SdkFunctionWrapper = defaultWrapper) {
  return {
    Deploy(variables: DeployQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<DeployQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<DeployQuery>(DeployDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Deploy', 'query');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;