package file

import (
	"strings"
	"dagger.io/dagger"
)

#Create: {
	filename:    !=""
	permissions: int | *0o644
	contents:    string | bytes

	#compute: [
		dagger.#WriteFile & {dest: filename, content: contents, mode: permissions},
	]
}

#Append: {
	filename:    !=""
	permissions: int | *0o644
	contents:    string | bytes
	from:        dagger.#Artifact

	orig: (#read & {path: filename, "from": from}).data

	#compute: [
		dagger.#WriteFile & {dest: filename, content: "\(orig)\(contents)", mode: permissions},
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
		#compute: [
			dagger.#Load & {"from":   from},
			dagger.#Export & {source: path},
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
		#compute: [
			dagger.#Load & {"from": from},
			dagger.#Exec & {
				args: ["sh", "-c", "ls \(glob) > \(_tmppath)"]
			},
			dagger.#Export & {source: _tmppath},
		]
	}
}
