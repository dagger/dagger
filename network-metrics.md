# Network metrics demo

## Start the demo

- Use a Linux machine that can run a privileged Dagger engine.
- Run this command from the Dagger repository:

  ```bash
  dagger shell -W github.com/dagger/dagger@pull/14159/head \
    engine-dev:playground
  ```

- In the new shell, run:

  ```bash
  ./hack/demo-network-metrics.sh
  ```

- Wait for `PASS`.

## Show the test operations

- Open `hack/demo-network-metrics/network-metrics-demo.dang`.
- Show `exec`.
- Point to the 32 MiB download from `peer`.
- Say: "This is traffic between two Dagger containers. It is internal."
- Point to the 8 MiB Cloudflare download and upload.
- Say: "This traffic leaves Dagger's networks. It is external."
- Point to `/dev/urandom` in the upload.
- Say: "Random data prevents compression from making the upload tiny."
- Show `httpDownload`, `gitPull`, `containerFrom`, and `registryPush`.
- Say: "These are Dagger operations, not commands running curl."
- Show `filesyncImport`, `filesyncExport`, `hostSocket`, and `tunnel`.
- Say: "These operations can share long-lived connections."

## Show the operation estimates

- In the output, find `PER-OPERATION ESTIMATES`.
- Point to the HTTP, Git, image pull, and image push rows.
- Point to the filesync, socket, and tunnel rows.
- Say: "These numbers assign application data to an operation."
- Say: "They are estimates. TCP, TLS, and HTTP headers are not all included."
- Say: "Several HTTP/2 streams can share one connection."

## Show the exact totals

- In the output, find `AUTHORITATIVE ENGINE CGROUP METRICS`.
- Point to the `userland` rows.
- Say: "Userland is traffic caused by user-requested work."
- Point to the `daggerland` rows.
- Say: "Daggerland is traffic needed by Dagger itself."
- Point to `internal` and `external`.
- Say: "Internal endpoints are on Dagger-managed networks. Everything else is
  external."
- Say: "eBPF counts full packets for the engine and its child cgroups."
- Say: "These totals include protocol overhead."

## Show the realm check

- In the output, find `UNATTRIBUTED ENGINE CGROUP TRAFFIC`.
- Say: "This is traffic from a socket that did not select userland or
  daggerland."
- Say: "A non-zero value finds a missing annotation. The main totals count it
  as userland, which is the conservative choice."
- Point to `realm enforcement`.
- Say: "When this is 1, new outbound TCP and UDP sockets created by the engine
  must have a realm before they connect."
- Point to `cgroup registrations`.
- Say: "User commands and helper processes get their realm from their cgroup."
- To require a realm in a controlled engine, set:

  ```bash
  _EXPERIMENTAL_DAGGER_NETWORK_REALM_ENFORCE=1
  ```

## Show why the old exec number was confusing

- In the output, find `PREVIOUS VIEW`.
- Point to `client Network Tx`.
- Say: "The old host-veth number merges the 32 MiB internal download and the
  8 MiB external download."
- Say: "Its receive and transmit names are also reversed because it observes
  the host side of the veth."
- In the output, find `NEW VIEW`.
- Point to `client Internal Rx` near 32 MiB.
- Point to `client External Rx` near 8 MiB.
- Point to `client External Tx` near 8 MiB.
- Point to `server Internal Tx` near 32 MiB.
- Say: "The new view uses the container's direction and keeps internal traffic
  separate."

## Show the implementation

- Open `engine/realm/realm.go`.
- Show `Realm.Dialer`, `Realm.Listen`, and `Transport`.
- Say: "Every engine connection chooses one fixed realm before its first
  packet."
- Say: "Userland and daggerland HTTP connections use separate pools."
- Open `internal/networkinglint/analyzer.go`.
- Show `forbiddenPackageFuncs` and `checkHTTPClient`.
- Say: "The repository check rejects direct dialers, listeners, and default
  HTTP clients."
- Open `engine/ebpf/nettracer/bpf/netbytes.bpf.c`.
- Show `packet_owner`, `add_owner_bytes`, and `allow_owned_connect`.
- Say: "Realm is represented as owner in the low-level eBPF maps."
- Say: "A child cgroup realm wins over a socket realm."
- Say: "Missing realms are counted separately and conservatively added to
  userland. New unattributed engine connections are rejected."
- Show `count_cgroup_ingress` and `count_cgroup_egress`.
- Say: "These programs count the engine and all child cgroups."
- Open `engine/ebpf/nettracer/tracer.go`.
- Show `addCurrentNetworkPrefixes`.
- Say: "The engine bridge subnet is internal. Host networking fails this
  check, so exact accounting becomes unavailable."
- Open `cmd/engine/metrics.go`.
- Show these metric names:
  - `dagger_network_accounting_available`
  - `dagger_network_bytes_total`
  - `dagger_network_unattributed_bytes_total`
  - `dagger_network_unattributed_packets_total`
  - `dagger_network_realm_enforced`

## If it fails

- If accounting availability is 0, check that the engine uses a veth bridge
  and can attach cgroup eBPF programs.
- If `playground` is unknown, check that the command uses the pull request
  version shown above.
- If an Internet request fails, check access to Docker Hub, GitHub, and
  Cloudflare Speed Test.
- If the tunnel fails, check that port `18080` is free.
