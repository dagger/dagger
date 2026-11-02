module difftest

go 1.25.0

require (
	go.etcd.io/bbolt v1.4.3
	realbolt v0.0.0
)

// go.etcd.io/bbolt is internal/fastbolt, and realbolt is a copy of the real
// bbolt with its import path renamed, put in place by test-split's
// testFastbolt check before it runs these tests.
replace go.etcd.io/bbolt => ../../internal/fastbolt

replace realbolt => ./realbolt
