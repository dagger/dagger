package profiler

import (
	"github.com/pkg/profile"
	"github.com/urfave/cli"
)

func Attach(app *cli.App) {
	app.Flags = append(app.Flags,
		cli.StringFlag{
			Name:   "profile-cpu",
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "profile-memory",
			Hidden: true,
		},
		cli.IntFlag{
			Name:   "profile-memoryrate",
			Value:  512 * 1024,
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "profile-block",
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "profile-mutex",
			Hidden: true,
		},
		cli.StringFlag{
			Name:   "profile-trace",
			Hidden: true,
		},
	)

	var stoppers = []interface {
		Stop()
	}{}

	before := app.Before
	app.Before = func(clicontext *cli.Context) error {
		if before != nil {
			if err := before(clicontext); err != nil {
				return err
			}
		}

		if cpuProfile := clicontext.String("profile-cpu"); cpuProfile != "" {
			stoppers = append(stoppers, profile.Start(profile.CPUProfile, profile.ProfilePath(cpuProfile), profile.NoShutdownHook))
		}

		if memProfile := clicontext.String("profile-memory"); memProfile != "" {
			stoppers = append(stoppers, profile.Start(profile.MemProfile, profile.ProfilePath(memProfile), profile.NoShutdownHook, profile.MemProfileRate(clicontext.Int("profile-memoryrate"))))
		}

		if blockProfile := clicontext.String("profile-block"); blockProfile != "" {
			stoppers = append(stoppers, profile.Start(profile.BlockProfile, profile.ProfilePath(blockProfile), profile.NoShutdownHook))
		}

		if mutexProfile := clicontext.String("profile-mutex"); mutexProfile != "" {
			stoppers = append(stoppers, profile.Start(profile.MutexProfile, profile.ProfilePath(mutexProfile), profile.NoShutdownHook))
		}

		if traceProfile := clicontext.String("profile-trace"); traceProfile != "" {
			stoppers = append(stoppers, profile.Start(profile.TraceProfile, profile.ProfilePath(traceProfile), profile.NoShutdownHook))
		}
		return nil
	}

	after := app.After
	app.After = func(clicontext *cli.Context) error {
		if after != nil {
			if err := after(clicontext); err != nil {
				return err
			}
		}

		for _, stopper := range stoppers {
			stopper.Stop()
		}
		return nil
	}
}
