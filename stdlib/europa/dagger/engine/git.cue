package engine

// Push a directory to a git remote
#GitPush: {
	@dagger(notimplemented)
	$dagger: task: _name: "GitPush"

	input:  #FS
	remote: string
	ref:    string
}

// Pull a directory from a git remote
// Note: do not add credentials to the remote url: e.g: https://username:password@github.com
// as this will expose those in logs. By using username and password (as #Secret) Dagger will
// url encode them for you
#GitPull: {
	$dagger: task: _name: "GitPull"
	remote:     string
	ref:        string
	keepGitDir: true | *false
	{
		username: string
		password: #Secret // can be password or personal access token
	} | {
		authToken: #Secret
	} | {
		authHeader: #Secret
	}
	output: #FS
}
