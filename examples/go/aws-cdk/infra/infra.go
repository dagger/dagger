package main

import (
	"github.com/aws/aws-cdk-go/awscdk/v2"

	"github.com/aws/jsii-runtime-go"
)

func main() {
	defer jsii.Close()
	app := awscdk.NewApp(nil)

	NewECRStack(app, "DaggerDemoECRStack", "dagger-cdk-demo")
	NewECSStack(app, "DaggerDemoECSStack")

	app.Synth(nil)
}
