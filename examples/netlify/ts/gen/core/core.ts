import { FS, Secret } from '@dagger.io/dagger'
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
  FS: FS;
  Secret: Secret;
};

export type Core = {
  __typename?: 'Core';
  copy: Scalars['FS'];
  dockerfile: Scalars['FS'];
  exec?: Maybe<CoreExec>;
  image?: Maybe<CoreImage>;
};


export type CoreCopyArgs = {
  dst?: InputMaybe<Scalars['FS']>;
  dstPath?: InputMaybe<Scalars['String']>;
  src: Scalars['FS'];
  srcPath?: InputMaybe<Scalars['String']>;
};


export type CoreDockerfileArgs = {
  context: Scalars['FS'];
  dockerfileName?: InputMaybe<Scalars['String']>;
};


export type CoreExecArgs = {
  input: CoreExecInput;
};


export type CoreImageArgs = {
  ref: Scalars['String'];
};

export type CoreExec = {
  __typename?: 'CoreExec';
  getMount: Scalars['FS'];
  root: Scalars['FS'];
};


export type CoreExecGetMountArgs = {
  path: Scalars['String'];
};

export type CoreExecInput = {
  args: Array<Scalars['String']>;
  mounts: Array<CoreMount>;
  workdir?: InputMaybe<Scalars['String']>;
};

export type CoreImage = {
  __typename?: 'CoreImage';
  fs: Scalars['FS'];
};

export type CoreMount = {
  fs: Scalars['FS'];
  path: Scalars['String'];
};

export type Exec = {
  __typename?: 'Exec';
  exitCode?: Maybe<Scalars['Int']>;
  fs: Filesystem;
  stderr?: Maybe<Scalars['String']>;
  stdout?: Maybe<Scalars['String']>;
};


export type ExecStderrArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};


export type ExecStdoutArgs = {
  lines?: InputMaybe<Scalars['Int']>;
};

export type Filesystem = {
  __typename?: 'Filesystem';
  dockerbuild: Filesystem;
  exec: Exec;
  file?: Maybe<Scalars['String']>;
  id: Scalars['ID'];
};


export type FilesystemDockerbuildArgs = {
  dockerfile?: InputMaybe<Scalars['String']>;
};


export type FilesystemExecArgs = {
  args?: InputMaybe<Array<Scalars['String']>>;
};


export type FilesystemFileArgs = {
  lines?: InputMaybe<Scalars['Int']>;
  path: Scalars['String'];
};

export type Mutation = {
  __typename?: 'Mutation';
  clientdir?: Maybe<Scalars['FS']>;
  import?: Maybe<Package>;
  readfile?: Maybe<Scalars['String']>;
  readsecret: Scalars['String'];
};


export type MutationClientdirArgs = {
  id: Scalars['String'];
};


export type MutationImportArgs = {
  fs?: InputMaybe<Scalars['FS']>;
  name: Scalars['String'];
};


export type MutationReadfileArgs = {
  fs: Scalars['FS'];
  path: Scalars['String'];
};


export type MutationReadsecretArgs = {
  input: Scalars['Secret'];
};

export type Package = {
  __typename?: 'Package';
  fs?: Maybe<Scalars['FS']>;
  name: Scalars['String'];
  operations: Scalars['String'];
  schema: Scalars['String'];
};

export type Query = {
  __typename?: 'Query';
  core: Core;
  source: Source;
};

export type Source = {
  __typename?: 'Source';
  git: Filesystem;
  image: Filesystem;
};


export type SourceGitArgs = {
  ref?: InputMaybe<Scalars['String']>;
  remote: Scalars['String'];
};


export type SourceImageArgs = {
  ref: Scalars['String'];
};

export type ImageQueryVariables = Exact<{
  ref: Scalars['String'];
}>;


export type ImageQuery = { __typename?: 'Query', core: { __typename?: 'Core', image?: { __typename?: 'CoreImage', fs: FS } | null } };

export type ExecQueryVariables = Exact<{
  input: CoreExecInput;
}>;


export type ExecQuery = { __typename?: 'Query', core: { __typename?: 'Core', exec?: { __typename?: 'CoreExec', root: FS } | null } };

export type ExecGetMountQueryVariables = Exact<{
  input: CoreExecInput;
  mountPath: Scalars['String'];
}>;


export type ExecGetMountQuery = { __typename?: 'Query', core: { __typename?: 'Core', exec?: { __typename?: 'CoreExec', getMount: FS } | null } };

export type DockerfileQueryVariables = Exact<{
  context: Scalars['FS'];
  dockerfileName: Scalars['String'];
}>;


export type DockerfileQuery = { __typename?: 'Query', core: { __typename?: 'Core', dockerfile: FS } };

export type ImportMutationVariables = Exact<{
  name: Scalars['String'];
  fs: Scalars['FS'];
}>;


export type ImportMutation = { __typename?: 'Mutation', import?: { __typename?: 'Package', name: string, schema: string, operations: string } | null };

export type ReadSecretMutationVariables = Exact<{
  input: Scalars['Secret'];
}>;


export type ReadSecretMutation = { __typename?: 'Mutation', readsecret: string };


export const ImageDocument = gql`
    query Image($ref: String!) {
  core {
    image(ref: $ref) {
      fs
    }
  }
}
    `;
export const ExecDocument = gql`
    query Exec($input: CoreExecInput!) {
  core {
    exec(input: $input) {
      root
    }
  }
}
    `;
export const ExecGetMountDocument = gql`
    query ExecGetMount($input: CoreExecInput!, $mountPath: String!) {
  core {
    exec(input: $input) {
      getMount(path: $mountPath)
    }
  }
}
    `;
export const DockerfileDocument = gql`
    query Dockerfile($context: FS!, $dockerfileName: String!) {
  core {
    dockerfile(context: $context, dockerfileName: $dockerfileName)
  }
}
    `;
export const ImportDocument = gql`
    mutation Import($name: String!, $fs: FS!) {
  import(name: $name, fs: $fs) {
    name
    schema
    operations
  }
}
    `;
export const ReadSecretDocument = gql`
    mutation ReadSecret($input: Secret!) {
  readsecret(input: $input)
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
    Import(variables: ImportMutationVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ImportMutation> {
      return withWrapper((wrappedRequestHeaders) => client.request<ImportMutation>(ImportDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Import', 'mutation');
    },
    ReadSecret(variables: ReadSecretMutationVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<ReadSecretMutation> {
      return withWrapper((wrappedRequestHeaders) => client.request<ReadSecretMutation>(ReadSecretDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'ReadSecret', 'mutation');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;