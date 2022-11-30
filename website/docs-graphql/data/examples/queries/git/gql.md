```gql
query {
  git(url: "https://github.com/dagger/dagger", keepGitDir: true) {
    branch(name: "main") {
      tree {
        file(path: ".git/refs/heads/main") {
          contents
        }
      }
    }
  }
}
```

<a href="https://play.dagger.cloud/playground/SlOyDOLjhja" target="_blank">Try it in the API Playground!</a>
