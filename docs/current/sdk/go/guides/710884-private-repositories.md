---
slug: /sdk/go/710884/private-repositories
displayed_sidebar: 'current'
---

# Use Dagger with Private Git Repositories

{@include: ../../../partials/_private-git-repository_first.md}

```go file=../snippets/private-repositories/main.go
```

{@include: ../../../partials/_private-git-repository_second.md}

Attempt to run the Go CI tool:

```shell
➜  go run .
panic: input:1: git.branch.tree.file.contents failed to load cache key: failed to fetch remote 
exit status 128
```

The CI tool fails, as it is unable to find the necessary authentication credentials to read the private repository in the SSH authentication agent.

Now, add the SSH key to the authentication agent on the host and try again:

```shell
➜ ssh-add
Identity added: xxxxx
go run .  
readme #
```

Finally, the CI tool succeeds in reading the private Git repository.
