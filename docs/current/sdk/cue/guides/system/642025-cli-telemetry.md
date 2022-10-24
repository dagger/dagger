---
slug: /sdk/cue/642025/cli-telemetry
displayed_sidebar: 'current'
---

# Understanding CLI Telemetry

## Overview

By default, the Dagger Engine sends anonymized telemetry to dagger.io. This allows us to improve the Dagger Engine by understanding how it is used.
Telemetry is optional and can be disabled at any time. If you are willing and able to leave telemetry enabled: thank you! This will help
us better understand how the Dagger Engine is used, and will allow us to improve your experience.

## What is tracked?

The following information is included in telemetry:

- Dagger Engine version
- Platform information
- Command run
- Anonymous device ID

We use telemetry for aggregate analysis, and do not tie telemetry events to a specific identity.

Our telemetry implementation is open-source and can be reviewed [here](https://github.com/dagger/dagger/blob/main/telemetry/telemetry.go).

## Disabling telemetry

The Dagger Engine implements the [(Console Do Not Track) standard](https://consoledonottrack.com/).

As a result, you can disable the telemetry by setting the environment variable `DO_NOT_TRACK=1` before running dagger.
