```gql
query {
  pipeline(name: "build", description: "Builds the app container") {
    container {
      id
    }
  }
}
```
