package main

import (
	"dagger.io/dagger"
	"github.com/goyek/goyek/v2"
)

func daggerClient(tf *goyek.TF) *dagger.Client {
	tf.Helper()
	c, err := dagger.Connect(tf.Context(), dagger.WithLogOutput(tf.Output()))
	if err != nil {
		tf.Fatal(err)
	}
	return c
}
