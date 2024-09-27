// TODO:
// TODO:
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
			"for i in $(seq 1 10); do dd if=/dev/random of=/f bs=1M count=1 && sync; sleep 1; done",
		}).
		WithExec([]string{"sh", "-c",
			"for i in $(seq 1 10); do dd if=/f of=/f2 iflag=direct oflag=direct bs=1M count=1 && sync; sleep 1; done",
		})
}
