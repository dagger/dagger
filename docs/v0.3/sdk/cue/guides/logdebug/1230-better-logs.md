---
slug: /1230/better-logs
displayed_sidebar: "0.2"
---

import DaggerCloudCTA from '../../includes/\_dagger-cloud-cta.md';

# How can I have better logs ?

Dagger exposes 2 logging format options:

- `--log-format <auto|plain|tty|json>`

The default mode is `auto`. If you want to keep each actions' logs, use the `plain` mode

- `--log-level <panic|fatal|error|warn|info|debug|trace>`

`debug` is useful to check whether an explicit dependency has been found between two actions and see CUE DAG at run time.

You can also export these options as env variables:

```console
export DAGGER_LOG_FORMAT="plain"
export DAGGER_LOG_LEVEL="debug"
```

<DaggerCloudCTA/>
