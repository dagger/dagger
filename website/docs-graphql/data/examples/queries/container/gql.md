```gql
query {
  container {
    from(address: "alpine") {
      defaultArgs
      entrypoint
      platform
      rootfs {
        entries
      }
    }
  }
}
```

<a href="https://play.dagger.cloud/playground/dp69svju5SR" target="_blank">Try it in the API Playground!</a>
