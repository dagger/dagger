---
slug: /710884/private-repositories
displayed_sidebar: 'current'
category: "guides"
tags: ["go"]
authors: ["Guillaume de Rouville"]
date: "31/10/2022"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Use Dagger with Private Git Repositories

Dagger recommends you to rely on your host's SSH authentication agent to securely authenticate against private remote Git repositories.

To clone private repositories, the only requirements are to run `ssh-add` on the Dagger host (to add your SSH key to the authentication agent), and mount its socket using the `SSHAuthSocket` parameter of the `(Dagger.GitRef).Tree` API.

Assume that you have a Dagger CI tool containing the following code, which references a private repository:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/private-repositories/main.go
```

</TabItem>
<TabItem value="Node.js (TypeScript)">

```typescript file=./snippets/private-repositories/clone.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/private-repositories/clone.py
```

</TabItem>
</Tabs>

Now, first remove all the SSH keys from the authentication agent on the Dagger host:

```shell
➜  ssh-add -D
All identities removed.
```

Attempt to run the CI tool:

<Tabs groupId="language">
<TabItem value="Go">

```shell
➜  go run .
panic: input:1: git.branch.tree.file.contents failed to load cache key: failed to fetch remote
exit status 128
```

</TabItem>
<TabItem value="Node.js (TypeScript)">

```shell
➜  node --loader ts-node/esm clone.ts
{'message': 'failed to load cache key: failed to fetch remote https://xxxxx@private-repository.git: exit status 128', 'locations': [{'line': 6, 'column': 11}], 'path': ['git', 'branch', 'tree', 'file', 'contents']}
```

</TabItem>
<TabItem value="Python">

```shell
➜  python clone.py
{'message': 'failed to load cache key: failed to fetch remote https://xxxxx@private-repository.git: exit status 128', 'locations': [{'line': 6, 'column': 11}], 'path': ['git', 'branch', 'tree', 'file', 'contents']}
```

</TabItem>
</Tabs>

The CI tool fails, as it is unable to find the necessary authentication credentials to read the private repository in the SSH authentication agent.

Now, add the SSH key to the authentication agent on the host and try again:

<Tabs groupId="language">
<TabItem value="Go">

```shell
➜ ssh-add
Identity added: xxxxx
go run .
readme #
```

</TabItem>
<TabItem value="Node.js (TypeScript)">

```shell
➜ ssh-add
Identity added: xxxxx
➜ node --loader ts-node/esm clone.ts
readme #
```

</TabItem>
<TabItem value="Python">

```shell
➜ ssh-add
Identity added: xxxxx
➜ python clone.py
readme #
```

</TabItem>
</Tabs>

Finally, the CI tool succeeds in reading the private Git repository.
