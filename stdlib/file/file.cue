package file

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/dagger/op"
)

#Create: {
	filename:    !=""           @dagger(input)
	permissions: int | *0o644   @dagger(input)
	contents:    string | bytes @dagger(input)

	#up: [
		op.#WriteFile & {dest: filename, content: contents, mode: permissions},
	]
}

#Append: {
	filename:    !=""             @dagger(input)
	permissions: int | *0o644     @dagger(input)
	contents:    string | bytes   @dagger(input)
	from:        dagger.#Artifact @dagger(input)

	orig: (#read & {path: filename, "from": from}).data @dagger(output)

	#up: [
		op.#WriteFile & {dest: filename, content: "\(orig)\(contents)", mode: permissions},
	]
}

#Read: {
	filename: !=""             @dagger(input)
	from:     dagger.#Artifact @dagger(input)
	contents: (#read & {path:  filename, "from": from}).data @dagger(output)
}

#read: {
	path: !=""             @dagger(input)
	from: dagger.#Artifact @dagger(input)
	data: {
		string
		#up: [
			op.#Load & {"from":   from},
			op.#Export & {source: path},
		]
	} @dagger(output)
}

#Glob: {
	glob: !="" @dagger(input)
	filenames: [...string] @dagger(input)
	from:  dagger.#Artifact   @dagger(input)
	files: (_#glob & {"glob": glob, "from": from}).data @dagger(output)
	// trim suffix because ls always ends with newline
	filenames: strings.Split(strings.TrimSuffix(files, "\n"), "\n") @dagger(output)
}

_#glob: {
	glob: !=""
	from: dagger.#Artifact
	data: {
		string
		_tmppath: "/tmp/ls.out"
		#up: [
			op.#Load & {"from": from},
			op.#Exec & {
				args: ["sh", "-c", "ls \(glob) > \(_tmppath)"]
			},
			op.#Export & {source: _tmppath},
		]
	}
}
