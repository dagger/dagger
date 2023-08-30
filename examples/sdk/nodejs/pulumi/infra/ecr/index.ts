import * as aws from "@pulumi/aws";

const repo = new aws.ecr.Repository("my-repo", {
  forceDelete: true,
});

const repoCreds = repo.registryId.apply(id => aws.ecr.getCredentials({ registryId: id }));

export const repositoryUrl = repo.repositoryUrl;
export const authorizationToken = repoCreds.authorizationToken;
