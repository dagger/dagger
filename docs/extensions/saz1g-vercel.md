---
slug: /saz1g/vercel
displayed_sidebar: '0.3'
---

# Vercel

A Dagger extension for Vercel deployment.

## Example

```graphql
query deploy(
  $tokenSecret: SecretID!
){
  netlifyDeploy(
      subdir: "."
      siteName: $siteName
      token: $tokenSecret
      build: "out/"
      teamId: "team_NHY3EF5Z6987pl2K1YiGtcg"

  ) {
      deployURL
  }
}
```

## Links

- [GitHub](https://github.com/slumbering/dagger-vercel)
