package python

// python image
// more configurable than the previous one
#ConfigurableImage: {
	repository: string | *"python"
	tag:        string | *"latest"

	docker.#Pull & {
		source: "\(repository):\(tag)"
	}
}
