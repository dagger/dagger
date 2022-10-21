import { client, DaggerServer, SecretID, gql } from "@dagger.io/dagger";

import { NetlifyAPI } from "netlify";
import { execa } from "execa";

import * as path from "path";

const resolvers = {
  Netlify: {
    deploy: async (args: {
      contents: string;
      subdir: string;
      siteName: string;
      token: SecretID;
      team: string;
    }) => {
      // TODO: should be set from Dockerfile ENV, just not propagated by dagger server yet
      process.env["PATH"] =
        "/src/examples/netlify/ts/node_modules/.bin:" + process.env["PATH"];
      process.env["HOME"] = "/tmp";

      const token = await client
        .request(
          gql`
            query GetSecretPlaintext($tokenID: SecretID!) {
              secret(id: $tokenID) {
                plaintext
              }
            }
          `,
          {
            tokenID: args.token,
          }
        )
        .then((result: any) => result.secret.plaintext);

      process.env["NETLIFY_AUTH_TOKEN"] = token;

      const netlifyClient = new NetlifyAPI(token);

      // filter the input site name out from the list of sites
      var site = await netlifyClient
        .listSites()
        .then((sites: Array<any>) =>
          sites.find((site: any) => site.name === args.siteName)
        );

      if (site === undefined) {
        // Create the site for a particular team
        if (args?.team && typeof args?.team === "string") {
          try {
            site = await netlifyClient.createSiteInTeam({
              account_slug: args.team,
              body: {
                name: args.siteName,
              },
            });
          } catch (error: any) {
            console.log(
              error?.status === 404
                ? `Unknown Netlify team ${args.team}`
                : error
            );
          }
        }
        site = await netlifyClient.createSite({
          body: {
            name: args.siteName,
          },
        });
      }

      if (!args.subdir) {
        args.subdir = "";
      }
      const srcDir = path.join("/mnt/contents", args.subdir);

      await execa("netlify", ["link", "--id", site.id], {
        stdout: "inherit",
        stderr: "inherit",
        cwd: srcDir,
      });

      await execa(
        "netlify",
        ["deploy", "--build", "--site", site.id, "--prod"],
        {
          stdout: "inherit",
          stderr: "inherit",
          cwd: srcDir,
        }
      );

      site = await netlifyClient.getSite({ site_id: site.id });
      return {
        url: site.url,
        deployURL: site.deploy_url,
      };
    },
  },
  Directory: {
    netlifyDeploy: async (
      args: {
        subdir: string;
        siteName: string;
        token: SecretID;
        team: string;
      },
      parent: { id: string }
    ) => {
      return client
        .request(
          gql`
            {
              netlify {
                deploy(contents: "${parent.id}", subdir: "${args.subdir}", siteName: "${args.siteName}", token: "${args.token}", team: "${args.team}") {
                  url
                  deployURL
                }
              }
            }
          `
        )
        .then((res: any) => res.netlify.deploy);
    },
  },
};

const server = new DaggerServer({ resolvers });

server.run();
