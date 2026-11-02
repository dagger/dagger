// Package execsched is the exec_sched eBPF program (see engine/ebpf/wcbpf).
// For each profiled exec it records the parts of its container's life as ops
// under the exec's exec.run op, from the kernel's process events, so without
// the engine's own reports (the exec.* phases) or a tracer's stops:
//
//   - exec_sched.containerSetup: from crun's child's creation in the
//     container's cgroup to its exec of the engine's init (/.init), or of the
//     workload in an exec without the init;
//   - exec_sched.initStart: from there to the workload's exec;
//   - exec_sched.workload: from there to the workload process's exit;
//   - exec_sched.initExit: from there to the init's exit.
//
// It also records, as the exec's exec_sched reading, the time all its threads
// (its container's, in every cgroup below it, and its mount helpers') waited
// runnable for a CPU, how many such waits ended, and the time they ran.
package execsched

// Name is how recordings ask for the program.
const Name = "exec_sched"

// The classes of the ops the program records.
const (
	ClassContainerSetup = "exec_sched.containerSetup"
	ClassInitStart      = "exec_sched.initStart"
	ClassWorkload       = "exec_sched.workload"
	ClassInitExit       = "exec_sched.initExit"
)
