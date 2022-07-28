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

export type Deploy = {
  __typename?: 'Deploy';
  deployUrl: Scalars['String'];
  logsUrl?: Maybe<Scalars['String']>;
  url: Scalars['String'];
};

export type Query = {
  __typename?: 'Query';
  todoapp: Todoapp;
};

export type Todoapp = {
  __typename?: 'Todoapp';
  build: Scalars['FS'];
  deploy: Deploy;
  test: Scalars['FS'];
};


export type TodoappBuildArgs = {
  src: Scalars['FS'];
};


export type TodoappDeployArgs = {
  src: Scalars['FS'];
  token: Scalars['Secret'];
};


export type TodoappTestArgs = {
  src: Scalars['FS'];
};

export type BuildQueryVariables = Exact<{
  src: Scalars['FS'];
}>;


export type BuildQuery = { __typename?: 'Query', todoapp: { __typename?: 'Todoapp', build: FS } };

export type TestQueryVariables = Exact<{
  src: Scalars['FS'];
}>;


export type TestQuery = { __typename?: 'Query', todoapp: { __typename?: 'Todoapp', test: FS } };


export const BuildDocument = gql`
    query Build($src: FS!) {
  todoapp {
    build(src: $src)
  }
}
    `;
export const TestDocument = gql`
    query Test($src: FS!) {
  todoapp {
    test(src: $src)
  }
}
    `;

export type SdkFunctionWrapper = <T>(action: (requestHeaders?:Record<string, string>) => Promise<T>, operationName: string, operationType?: string) => Promise<T>;


const defaultWrapper: SdkFunctionWrapper = (action, _operationName, _operationType) => action();

export function getSdk(client: GraphQLClient, withWrapper: SdkFunctionWrapper = defaultWrapper) {
  return {
    Build(variables: BuildQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<BuildQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<BuildQuery>(BuildDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Build', 'query');
    },
    Test(variables: TestQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<TestQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<TestQuery>(TestDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Test', 'query');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;