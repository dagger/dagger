package task

import (
	"context"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	// Register("Start", func() Task { return &startTask{} })
	// Register("Stop", func() Task { return &stopTask{} })
	// Register("SendSignal", func() Task { return &sendSignalTask{} })
}

type startTask struct {
}

func (t *startTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	// common, err := parseCommon(pctx, v)
	// if err != nil {
	// 	return nil, err
	// }
	// req, err := common.containerRequest()
	// if err != nil {
	// 	return nil, err
	// }

	// // env
	// envVal, err := v.Lookup("env").Fields()
	// if err != nil {
	// 	return nil, err
	// }

	// for _, env := range envVal {
	// 	s, err := env.Value.String()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	req.Proc.Env = append(req.Proc.Env, fmt.Sprintf("%s=%s", env.Label(), s))
	// }

	// // platform
	// platform := pb.PlatformFromSpec(pctx.Platform.Get())
	// req.Container.Platform = &platform

	// req.Name = v.Path().String()
	// ctrID, err := s.StartContainer(ctx, req)
	// if err != nil {
	// 	return nil, err
	// }

	// lg := log.Ctx(ctx)
	// lg.Debug().Msgf("started exec %s", ctrID)

	// // Fill result
	// if err := v.FillPath(cue.MakePath(cue.Hid("_id", pkg.DaggerPackage)), ctrID); err != nil {
	// 	return nil, err
	// }
	// return v, nil
	return nil, nil
}

type stopTask struct {
}

func (t *stopTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	// ctrID, err := v.LookupPath(cue.MakePath(cue.Str("input"), cue.Hid("_id", pkg.DaggerPackage))).String()
	// if err != nil {
	// 	return nil, err
	// }

	// name, err := s.ContainerName(ctrID)
	// if err != nil {
	// 	return nil, err
	// }

	// timeout, err := v.Lookup("timeout").Int64()
	// if err != nil {
	// 	return nil, err
	// }

	// lg := log.Ctx(ctx)

	// exitCode, err := s.StopContainer(ctx, ctrID, time.Duration(timeout))
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to stop %s: %w", name, err)
	// }
	// lg.Debug().Msgf("exec %s stopped with exit code %d", ctrID, exitCode)

	// return compiler.NewValue().FillFields(map[string]interface{}{
	// 	"exit": exitCode,
	// })
	return nil, nil
}

type sendSignalTask struct {
}

func (t *sendSignalTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	// ctrID, err := v.LookupPath(cue.MakePath(cue.Str("input"), cue.Hid("_id", pkg.DaggerPackage))).String()
	// if err != nil {
	// 	return nil, err
	// }

	// name, err := s.ContainerName(ctrID)
	// if err != nil {
	// 	return nil, err
	// }

	// sigVal, err := v.Lookup("signal").Int64()
	// if err != nil {
	// 	return nil, err
	// }
	// sig := syscall.Signal(sigVal)

	// if err := s.SignalContainer(ctx, ctrID, sig); err != nil {
	// 	return nil, fmt.Errorf("failed to send signal %d to %s: %w", sig, name, err)
	// }
	// log.Ctx(ctx).Debug().Msgf("sent signal %d to exec %s", sig, ctrID)

	// return compiler.NewValue(), nil
	return nil, nil
}

// func (e execCommon) containerRequest() (solver.StartContainerRequest, error) {
// 	req := solver.StartContainerRequest{
// 		Container: client.NewContainerRequest{
// 			Mounts: []client.Mount{{
// 				Dest:      "/",
// 				MountType: pb.MountType_BIND,
// 				Ref:       e.root.Result(),
// 			}},
// 		},
// 		Proc: client.StartRequest{
// 			Args: e.args,
// 			User: e.user,
// 			Cwd:  e.workdir,
// 		},
// 	}

// 	for _, mnt := range e.mounts {
// 		m, err := mnt.containerMount()
// 		if err != nil {
// 			return req, err
// 		}
// 		req.Container.Mounts = append(req.Container.Mounts, m)
// 	}

// 	for k, v := range e.hosts {
// 		req.Container.ExtraHosts = append(req.Container.ExtraHosts, &pb.HostIP{Host: k, IP: v})
// 	}

// 	return req, nil
// }
