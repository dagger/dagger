package dagger

import "fmt"

const (
	errorHelpBlurb = "Please visit https://dagger.io/help#go for troubleshooting guidance."
)

func withErrorHelp(err error) error {
	return fmt.Errorf("%w\n%s", err, errorHelpBlurb)
}
