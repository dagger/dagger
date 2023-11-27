```gql
query {
  pipeline(name: "build", description: "Builds the app container") {
    container {
      id
    }
  }
}
```

<a href="https://play.dagger.cloud/playground/JqqwzSV0wBO" target="_blank">Try it in the API Playground!</a>
