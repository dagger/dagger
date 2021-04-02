package file

import (
	"strings"
	"dagger.io/dagger"
	"dagger.io/llb"
)

#Create: {
	filename:    !=""
	permissions: int | *0o644
	contents:    string | bytes

	#up: [
		llb.#WriteFile & {dest: filename, content: contents, mode: permissions},
	]
}

#Append: {
	filename:    !=""
	permissions: int | *0o644
	contents:    string | bytes
	from:        dagger.#Artifact

	orig: (#read & {path: filename, "from": from}).data

	#up: [
		llb.#WriteFile & {dest: filename, content: "\(orig)\(contents)", mode: permissions},
	]
}

#Read: {
	filename: !=""
	from:     dagger.#Artifact
	contents: (#read & {path: filename, "from": from}).data
}

#read: {
	path: !=""
	from: dagger.#Artifact
	data: {
		string
		#up: [
			llb.#Load & {"from":   from},
			llb.#Export & {source: path},
		]
	}
}

#Glob: {
	glob: !=""
	filenames: [...string]
	from:  dagger.#Artifact
	files: (_#glob & {"glob": glob, "from": from}).data
	// trim suffix because ls always ends with newline
	filenames: strings.Split(strings.TrimSuffix(files, "\n"), "\n")
}

_#glob: {
	glob: !=""
	from: dagger.#Artifact
	data: {
		string
		_tmppath: "/tmp/ls.out"
		#up: [
			llb.#Load & {"from": from},
			llb.#Exec & {
				args: ["sh", "-c", "ls \(glob) > \(_tmppath)"]
			},
			llb.#Export & {source: _tmppath},
		]
	}
}
