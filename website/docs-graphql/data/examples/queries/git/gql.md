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
