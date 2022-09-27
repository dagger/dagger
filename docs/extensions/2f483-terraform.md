---
slug: /2f483/terraform
displayed_sidebar: '0.3'
---

# Terraform

A Dagger extension for Terraform deployment.

## Example

```
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

- [GitHub](https://github.com/kpenfound/dagger-terraform)