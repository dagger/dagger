setup() {
	load 'helpers'

	common_setup
}

@test "project init" {
	cd "$TESTDIR"
	# mkdir -p ./project/init
	"$DAGGER" project init ./project/init --name "github.com/foo/bar"
	test -d ./project/init/cue.mod/pkg
	test -d ./project/init/cue.mod/usr
	test -f ./project/init/cue.mod/module.cue
	contents=$(cat ./project/init/cue.mod/module.cue)
	[ "$contents" == 'module: "github.com/foo/bar"' ]
}