---
slug: /1218/cli-telemetry
displayed_sidebar: '0.2'
---

# Understanding CLI Telemetry

## Overview

By default, the dagger CLI sends anonymized telemetry to dagger.io. This allows us to improve Dagger by understanding how it is used.
Telemetry is optional and can be disabled at any time. If you are willing and able to leave telemetry enabled: thank you! This will help
us better understand how Dagger is used, and will allow us to improve your experience.

## What is tracked?

The following information is included in telemetry:

- Dagger version
- Platform information
- Command run
- Anonymous device ID

We use telemetry for aggregate analysis, and do not tie telemetry events to a specific identity.

Our telemetry implementation is open-source and can be reviewed [here](https://github.com/dagger/dagger/blob/main/telemetry/telemetry.go).

## Disabling telemetry

Dagger implements the [(Console Do Not Track) standard](https://consoledonottrack.com/).

As a result, you can disable the telemetry by setting the environment variable `DO_NOT_TRACK=1` before running dagger.
