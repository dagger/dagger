package main

import "context"

type Test struct{}

func (m *Test) DriveRollsRoyce(ctx context.Context) error {
	return dag.Drive(dag.RollsRoyce().AsDriveCar()).DriveIt(ctx)
}
