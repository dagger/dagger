hashring
============================

Implements consistent hashing that can be used when
the number of server nodes can increase or decrease (like in memcached).
The hashing ring is built using the same algorithm as libketama.

This is a port of Python hash_ring library <https://pypi.python.org/pypi/hash_ring/>
in Go with the extra methods to add and remove nodes.


Using
============================

Importing ::

```go
import "github.com/serialx/hashring"
```

Basic example usage ::

```go
memcacheServers := []string{"192.168.0.246:11212",
                            "192.168.0.247:11212",
                            "192.168.0.249:11212"}

ring := hashring.New(memcacheServers)
server, _ := ring.GetNode("my_key")
```

To fulfill replication requirements, you can also get a list of servers that should store your key.
```go
serversInRing := []string{"192.168.0.246:11212",
                          "192.168.0.247:11212",
                          "192.168.0.248:11212",
                          "192.168.0.249:11212",
                          "192.168.0.250:11212",
                          "192.168.0.251:11212",
                          "192.168.0.252:11212"}

replicaCount := 3
ring := hashring.New(serversInRing)
server, _ := ring.GetNodes("my_key", replicaCount)
```

Using weights example ::

```go
weights := make(map[string]int)
weights["192.168.0.246:11212"] = 1
weights["192.168.0.247:11212"] = 2
weights["192.168.0.249:11212"] = 1

ring := hashring.NewWithWeights(weights)
server, _ := ring.GetNode("my_key")
```

Adding and removing nodes example ::

```go
memcacheServers := []string{"192.168.0.246:11212",
                            "192.168.0.247:11212",
                            "192.168.0.249:11212"}

ring := hashring.New(memcacheServers)
ring = ring.RemoveNode("192.168.0.246:11212")
ring = ring.AddNode("192.168.0.250:11212")
server, _ := ring.GetNode("my_key")
```
