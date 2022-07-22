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
  FS: any;
};

export type Alpine = {
  __typename?: 'Alpine';
  build: Scalars['FS'];
};


export type AlpineBuildArgs = {
  pkgs: Array<Scalars['String']>;
};

export type Query = {
  __typename?: 'Query';
  alpine: Alpine;
};

export type BuildQueryVariables = Exact<{
  pkgs: Array<Scalars['String']> | Scalars['String'];
}>;


export type BuildQuery = { __typename?: 'Query', alpine: { __typename?: 'Alpine', build: any } };


export const BuildDocument = gql`
    query Build($pkgs: [String!]!) {
  alpine {
    build(pkgs: $pkgs)
  }
}
    `;

export type SdkFunctionWrapper = <T>(action: (requestHeaders?:Record<string, string>) => Promise<T>, operationName: string, operationType?: string) => Promise<T>;


const defaultWrapper: SdkFunctionWrapper = (action, _operationName, _operationType) => action();

export function getSdk(client: GraphQLClient, withWrapper: SdkFunctionWrapper = defaultWrapper) {
  return {
    Build(variables: BuildQueryVariables, requestHeaders?: Dom.RequestInit["headers"]): Promise<BuildQuery> {
      return withWrapper((wrappedRequestHeaders) => client.request<BuildQuery>(BuildDocument, variables, {...requestHeaders, ...wrappedRequestHeaders}), 'Build', 'query');
    }
  };
}
export type Sdk = ReturnType<typeof getSdk>;