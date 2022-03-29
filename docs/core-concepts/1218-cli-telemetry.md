---
slug: /1218/cli-telemetry
displayed_sidebar: europa
---

# CLI Telemetry

The dagger CLI implements telemetry that provides information about the usage from the Dagger Community. Although optional, this helps us to improve dagger by measuring the usage of the main features.

## What is tracked?

Command names are tracked along with the version of dagger and the platform it's running on. CLI telemetry events are anonymized for privacy purposes, they are not linked to a specific identity.

If you want to know more, you can check the telemetry implementation in dagger's source code.

## Can I disable the telemetry?

Dagger implements [the DNT (Console Do Not Track) standard](https://consoledonottrack.com/).

As a result, you can disable the telemetry by setting the environment variable `DO_NOT_TRACK=1` before running dagger.
