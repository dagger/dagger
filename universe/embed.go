//go:generate sh -e -x -c "rm -f universe.tar && tar -C ../ --create --file universe.tar --exclude '.git/*' --exclude 'bin/*' ."
package universe

import (
	_ "embed"
)

//go:embed universe.tar
var Tar []byte
