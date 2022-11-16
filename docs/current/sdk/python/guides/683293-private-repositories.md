---
slug: /683293/private-repositories
displayed_sidebar: "current"
---

# Use Dagger with Private Git Repositories

{@include: ../../../partials/_private-git-repository_first.md}

```python file=../snippets/private-repositories/clone.py
```

{@include: ../../../partials/_private-git-repository_second.md}

Attempt to run the Python CI tool:

```shell
➜  python clone.py
{'message': 'failed to load cache key: failed to fetch remote https://xxxxx@private-repository.git: exit status 128', 'locations': [{'line': 6, 'column': 11}], 'path': ['git', 'branch', 'tree', 'file', 'contents']}
```

The CI tool fails, as it is unable to find the necessary authentication credentials to read the private repository in the SSH authentication agent.

Now, add the SSH key to the authentication agent on the host and try again:

```shell
➜ ssh-add
Identity added: xxxxx
➜ python clone.py
readme #
```

Finally, the CI tool succeeds in reading the private Git repository.
