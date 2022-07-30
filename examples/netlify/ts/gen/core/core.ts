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

export type Mutation = {
  __typename?: 'Mutation';
  import?: Maybe<Package>;
};


export type MutationImportArgs = {
  fs?: InputMaybe<Scalars['FSID']>;
  name: Scalars['String'];
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
  core: Core;
};

export type ImageQueryVariables = Exact<{
  ref: Scalars['String'];
}>;


export type ImageQuery = { __typename?: 'Query', core: { __typename?: 'Core', image: { __typename?: 'Filesystem', id: FSID } } };

export type ExecQueryVariables = Exact<{
  fsid: Scalars['FSID'];
  input: ExecInput;
}>;


export type ExecQuery = { __typename?: 'Query', core: { __typename?: 'Core', filesystem: { __typename?: 'Filesystem', exec: { __typename?: 'Exec', fs: { __typename?: 'Filesystem', id: FSID } } } } };

export type ExecGetMountQueryVariables = Exact<{
  fsid: Scalars['FSID'];
  input: ExecInput;
  getPath: Scalars['String'];
}>;


export type ExecGetMountQuery = { __typename?: 'Query', core: { __typename?: 'Core', filesystem: { __typename?: 'Filesystem', exec: { __typename?: 'Exec', mount: { __typename?: 'Filesystem', id: FSID } } } } };

export type DockerfileQueryVariables = Exact<{
  context: Scalars['FSID'];
  dockerfileName: Scalars['String'];
}>;


export type DockerfileQuery = { __typename?: 'Query', core: { __typename?: 'Core', filesystem: { __typename?: 'Filesystem', dockerbuild: { __typename?: 'Filesystem', id: FSID } } } };

export type SecretQueryVariables = Exact<{
  id: Scalars['SecretID'];
}>;


export type SecretQuery = { __typename?: 'Query', core: { __typename?: 'Core', secret: string } };


export const ImageDocument = gql`
    query Image($ref: String!) {
  core {
    image(ref: $ref) {
      id
    }
  }
}
    `;
export const ExecDocument = gql`
    query Exec($fsid: FSID!, $input: ExecInput!) {
  core {
    filesystem(id: $fsid) {
      exec(input: $input) {
        fs {
          id
        }
      }
    }
  }
}
    `;
export const ExecGetMountDocument = gql`
    query ExecGetMount($fsid: FSID!, $input: ExecInput!, $getPath: String!) {
  core {
    filesystem(id: $fsid) {
      exec(input: $input) {
        mount(path: $getPath) {
          id
        }
      }
    }
  }
}
    `;
export const DockerfileDocument = gql`
    query Dockerfile($context: FSID!, $dockerfileName: String!) {
  core {
    filesystem(id: $context) {
      dockerbuild(dockerfile: $dockerfileName) {
        id
      }
    }
  }
}
    `;
export const SecretDocument = gql`
    query Secret($id: SecretID!) {
  core {
    secret(id: $id)
  }
}
    `;

export type SdkFunctionWrapper = <T>(action: (requestHeaders?:Record<string, string>) => Promise<T>, operationName: string, operationType?: string) => Promise<T>;


const defaultWrapper: SdkFunctionWrapper = (action, _operationName, _operationType) => action();

export function getSdk(client: GraphQLClient, withWrapper: SdkFunctionWrapper = defaultWrapper) {
  return {
    Image(variables: ImageQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ImageQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<ImageQuery>(ImageDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Image', 'query');
    },
    Exec(variables: ExecQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ExecQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<ExecQuery>(ExecDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Exec', 'query');
    },
    ExecGetMount(variables: ExecGetMountQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ExecGetMountQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<ExecGetMountQuery>(ExecGetMountDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'ExecGetMount', 'query');
    },
    Dockerfile(variables: DockerfileQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<DockerfileQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<DockerfileQuery>(DockerfileDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Dockerfile', 'query');
    },
    Secret(variables: SecretQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<SecretQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<SecretQuery>(SecretDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Secret', 'query');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;