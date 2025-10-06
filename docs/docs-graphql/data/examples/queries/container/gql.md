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
