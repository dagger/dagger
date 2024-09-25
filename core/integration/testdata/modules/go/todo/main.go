// TODO:
// TODO:
// TODO:
// TODO:
// TODO:
// TODO:
// TODO:
// TODO: RM

package main

import (
	"dagger/test/internal/dagger"
	"fmt"
	"time"
)

type Test struct{}

func (m *Test) Foo() *dagger.Container {
	return dag.Container().From("alpine:3.19").
		WithEnvVariable("CACHEBUST", fmt.Sprintf("%d", time.Now().UnixNano())).
		WithExec([]string{"sh", "-c",
			"for i in $(seq 1 10); do echo dd if=/dev/zero of=/f bs=1M count=100 && sync; sleep 2; done",
			// "dd if=/dev/zero of=/bigfile bs=1M count=100 && sync && sleep 30",
		})
}
