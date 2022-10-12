package main

// Deployment template containing all the common boilerplate shared by
// deployments of this application.
#Deployment: {
	// Name of the deployment. This will be used to label resources automatically
	// and generate selectors.
	name: string

	// Container image.
	image: string

	// 80 is the default port.
	port: *80 | int

	// 1 is the default, but we allow any number.
	replicas: *1 | int

	// Deployment manifest. Uses the name, image, port and replicas above to
	// generate the resource manifest.
	manifest: {
		apiVersion: "apps/v1"
		kind:       "Deployment"
		metadata: {
			"name": name
			labels: app: name
		}
		spec: {
			"replicas": replicas
			selector: matchLabels: app: name
			template: {
				metadata: labels: app: name
				spec: containers: [{
					"name":  name
					"image": image
					ports: [{
						containerPort: port
					}]
				}]
			}
		}
	}
}
