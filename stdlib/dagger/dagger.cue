package dagger

// An artifact such as source code checkout, container image, binary archive...
// May be passed as user input, or computed by a buildkit pipeline
#Artifact: _ @dagger(artifact)

// An encrypted secret
#Secret: _ @dagger(secret)

// A computed value
#Computed: _ @dagger(computed)

// A dagger relay
#Relay: _ @dagger(relay)
