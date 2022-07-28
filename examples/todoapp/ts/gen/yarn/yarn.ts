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

export type Query = {
  __typename?: 'Query';
  yarn: Yarn;
};

export type Yarn = {
  __typename?: 'Yarn';
  script: Scalars['FS'];
};


export type YarnScriptArgs = {
  name?: InputMaybe<Scalars['String']>;
  source: Scalars['FS'];
};

export type ScriptQueryVariables = Exact<{
  source: Scalars['FS'];
  name: Scalars['String'];
}>;


export type ScriptQuery = { __typename?: 'Query', yarn: { __typename?: 'Yarn', script: FS } };


export const ScriptDocument = gql`
    query Script($source: FS!, $name: String!) {
  yarn {
    script(source: $source, name: $name)
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