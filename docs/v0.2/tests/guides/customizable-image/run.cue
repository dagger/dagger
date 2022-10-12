package python

// Like #RunBase, but with a pre-configured container image.
#Run: #RunBase & {
	_image: #Image
	image:  _image.output
}
