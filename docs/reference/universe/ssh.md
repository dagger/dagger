---
sidebar_label: ssh
---

# alpha.dagger.io/ssh

```cue
import "alpha.dagger.io/ssh"
```

## ssh.#Files

Upload files or secrets to remote host

### ssh.#Files Inputs

| Name               | Type                | Description        |
| -------------      |:-------------:      |:-------------:     |
|*sshConfig.host*    | `string`            |ssh host            |
|*sshConfig.user*    | `string`            |ssh user            |
|*sshConfig.port*    | `*22 \| int`        |ssh port            |
|*sshConfig.key*     | `dagger.#Secret`    |private key         |

### ssh.#Files Outputs

_No output._
