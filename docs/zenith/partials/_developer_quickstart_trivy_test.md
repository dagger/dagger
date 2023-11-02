  ```shell
  dagger call  scan-image --image-ref alpine:latest
  ```

  Here's an example of the output:

  ```shell
  ✔ dagger call scan-image [5.28s]
  ┃
  ┃ alpine:latest (alpine 3.18.4)
  ┃ =============================
  ┃ Total: 2 (UNKNOWN: 0, LOW: 0, MEDIUM: 2, HIGH: 0, CRITICAL: 0)
  ┃
  ┃ ┌────────────┬───────────────┬──────────┬────────┬───────────────────┬───────────────┬───────────────────────────────────────────────┐
  ┃ │  Library   │ Vulnerability │ Severity │ Status │ Installed Version │ Fixed Version │                     Title                     │
  ┃ ├────────────┼───────────────┼──────────┼────────┼───────────────────┼───────────────┼───────────────────────────────────────────────┤
  ┃ │ libcrypto3 │ CVE-2023-5363 │ MEDIUM   │ fixed  │ 3.1.3-r0          │ 3.1.4-r0      │ Incorrect cipher key and IV length processing │
  ┃ │            │               │          │        │                   │               │ https://avd.aquasec.com/nvd/cve-2023-5363     │
  ┃ ├────────────┤               │          │        │                   │               │                                               │
  ┃ │ libssl3    │               │          │        │                   │               │                                               │
  ┃ │            │               │          │        │                   │               │                                               │
  ┃ └────────────┴───────────────┴──────────┴────────┴───────────────────┴───────────────┴───────────────────────────────────────────────┘
  • Engine: 8212a964c511 (version v0.9.2)
  ⧗ 33.46s ✔ 65 ∅ 4
  ```
