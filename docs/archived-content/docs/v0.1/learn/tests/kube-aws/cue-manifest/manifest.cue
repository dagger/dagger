package main

import (
	"encoding/yaml"
)

// Define and generate kubernetes deployment to deploy to kubernetes cluster
#AppManifest: {
	// Name of the application
	name: string

	// Image to deploy to
	image: string

	// Define a kubernetes deployment object
	deployment: #Deployment & {
		"name":  name
		"image": image
	}

	// Define a kubernetes service object
	service: #Service & {
		"name": name
		ports: http: deployment.port
	}

	// Merge definitions and convert them back from CUE to YAML
	manifest: yaml.MarshalStream([deployment.manifest, service.manifest])
}
