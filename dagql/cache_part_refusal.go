package dagql

// partRefusal is a refusal in the reselect class that names the site that made
// it. It unwraps to ErrPartReselect, so every retry loop and every errors.Is
// caller treats it exactly as the bare sentinel. The site is what the reselect
// watch's warning reports, so a loop that is not making progress says where it
// is refused; continuation-evidence's reselect audit lists every site by this
// name with its class.
type partRefusal struct {
	site string
	// cause is the error the site answered with a reselect, kept as text: the
	// refusal's class is ErrPartReselect whatever the cause was.
	cause error
}

func (r *partRefusal) Error() string {
	if r.cause != nil {
		return ErrPartReselect.Error() + ": " + r.site + ": " + r.cause.Error()
	}
	return ErrPartReselect.Error() + ": " + r.site
}

func (r *partRefusal) Unwrap() error { return ErrPartReselect }

func partRefused(site string) error { return &partRefusal{site: site} }

func partRefusedBy(site string, cause error) error { return &partRefusal{site: site, cause: cause} }
