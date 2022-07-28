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

/** Netlify Deployment */
export type Deploy = {
  __typename?: 'Deploy';
  /** Unique URL for this deployment */
  deployUrl: Scalars['String'];
  /** Deployment Logs */
  logsUrl?: Maybe<Scalars['String']>;
  /** Production URL of the deployed site */
  url: Scalars['String'];
};

/** Netlify Action */
export type Netlify = {
  __typename?: 'Netlify';
  /** Deploy a site to Netlify */
  deploy: Deploy;
};


/** Netlify Action */
export type NetlifyDeployArgs = {
  contents: Scalars['FS'];
  siteName?: InputMaybe<Scalars['String']>;
  subdir?: InputMaybe<Scalars['String']>;
  token: Scalars['Secret'];
};

export type Query = {
  __typename?: 'Query';
  /** Netlify Action */
  netlify: Netlify;
};

export type DeployQueryVariables = Exact<{
  contents: Scalars['FS'];
  subdir?: InputMaybe<Scalars['String']>;
  siteName?: InputMaybe<Scalars['String']>;
  token: Scalars['Secret'];
}>;


export type DeployQuery = { __typename?: 'Query', netlify: { __typename?: 'Netlify', deploy: { __typename?: 'Deploy', url: string, deployUrl: string } } };


export const DeployDocument = gql`
    query Deploy($contents: FS!, $subdir: String, $siteName: String, $token: Secret!) {
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