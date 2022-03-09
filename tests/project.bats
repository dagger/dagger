setup() {
	load 'helpers'

	common_setup
}

# @test "project init and update" {
# 	TEMPDIR=$(mktemp -d)
# 	echo "TEMPDIR=$TEMPDIR"
# 	cd "$TEMPDIR"
#
# 	"$DAGGER" project init ./ --name "github.com/foo/bar"
# 	test -d ./cue.mod/pkg
# 	test -d ./cue.mod/usr
# 	test -f ./cue.mod/module.cue
# 	contents=$(cat ./cue.mod/module.cue)
# 	[ "$contents" == 'module: "github.com/foo/bar"' ]
#
# 	dagger project update
# 	test -d ./cue.mod/pkg/dagger.io
# 	test -d ./cue.mod/pkg/universe.dagger.io
# }
