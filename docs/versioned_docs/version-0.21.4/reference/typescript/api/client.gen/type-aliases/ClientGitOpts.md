---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ClientGitOpts

> **ClientGitOpts** = `object`

## Properties

### experimentalServiceHost?

> `optional` **experimentalServiceHost?**: [`Service`](../classes/Service.md)

A service which must be started before the repo is fetched.

***

### httpAuthHeader?

> `optional` **httpAuthHeader?**: [`Secret`](../classes/Secret.md)

Secret used to populate the Authorization HTTP header

***

### httpAuthToken?

> `optional` **httpAuthToken?**: [`Secret`](../classes/Secret.md)

Secret used to populate the password during basic HTTP Authorization

***

### httpAuthUsername?

> `optional` **httpAuthUsername?**: `string`

Username used to populate the password during basic HTTP Authorization

***

### ~~keepGitDir?~~

> `optional` **keepGitDir?**: `boolean`

DEPRECATED: Set to true to keep .git directory.

#### Deprecated

Set to true to keep .git directory.

***

### sshAuthSocket?

> `optional` **sshAuthSocket?**: [`Socket`](../classes/Socket.md)

Set SSH auth socket

***

### sshKnownHosts?

> `optional` **sshKnownHosts?**: `string`

Set SSH known hosts
