package main

import "context"

type Drive struct {
	Car Car
}

func New(car Car) *Drive {
	return &Drive{Car: car}
}

func (d *Drive) DriveIt(ctx context.Context) error {
	return d.Car.Drive(ctx)
}

type Car interface {
	DaggerObject
	Drive(ctx context.Context) error
}
