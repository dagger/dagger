package attrs

import (
	"strings"
)

#Account: {
	id: string @input(help="account id")
	apikey: string @input(help="apikey")
}

#Site: {
	acct: #Account
	name: string @input(help="name of site")
	url: string & strings.MinRunes(2)
	deployURL: string @output(help="URL returned for the deployment")
}


