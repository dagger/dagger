package dagui

import "github.com/dagger/dagger/dagql/call/callpbv1"

// Argument spans.
//
// A row renders its call's ID-valued arguments through the spans that ran
// them and the spans that produced them: an argument shows as its creator's
// call (DB.Simplify, by output digest) and through its most interesting
// span (DB.MostInterestingSpan, by call digest), whose base it renders. So
// the spans carrying, or producing, an ID that a retained span's call takes
// as an argument, at any depth, are retained whatever the policy: those
// folded already are read back, those to come are retained on arrival.

// indexDigests records folded span i under the call it carries and the
// output it produced, for wantArgs to find.
func (f *Fold) indexDigests(i uint32, info foldInfo) {
	for _, dig := range [2]string{info.callDigest, info.output} {
		if dig == "" {
			continue
		}
		if f.byDigest == nil {
			f.byDigest = map[string][]uint32{}
		}
		f.byDigest[dig] = append(f.byDigest[dig], i)
	}
}

// isWanted reports whether an arriving span carries or produced an ID a
// retained span's call takes as an argument.
func (f *Fold) isWanted(info foldInfo) bool {
	if len(f.wanted) == 0 {
		return false
	}
	for _, dig := range [2]string{info.callDigest, info.output} {
		if _, ok := f.wanted[dig]; ok && dig != "" {
			return true
		}
	}
	return false
}

// wantArgs retains the spans of the IDs the retained span's call (dig, or
// its payload if the DB has no call for it yet) takes as arguments, at any
// depth.
func (f *Fold) wantArgs(dig, payload string) {
	if dig == "" {
		return
	}
	call := f.db.Calls[dig]
	if call == nil {
		if payload == "" {
			return
		}
		call = new(callpbv1.Call)
		if err := call.Decode(payload); err != nil {
			return
		}
	}
	var stack []*callpbv1.Literal
	pushArgs := func(c *callpbv1.Call) {
		for _, arg := range c.GetArgs() {
			stack = append(stack, arg.GetValue())
		}
	}
	pushArgs(call)
	for len(stack) > 0 {
		lit := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, item := range lit.GetList().GetValues() {
			stack = append(stack, item)
		}
		for _, item := range lit.GetObject().GetValues() {
			stack = append(stack, item.GetValue())
		}
		argDig := lit.GetCallDigest()
		if argDig == "" {
			continue
		}
		if _, ok := f.wanted[argDig]; ok {
			continue
		}
		if f.wanted == nil {
			f.wanted = map[string]struct{}{}
		}
		f.wanted[argDig] = struct{}{}
		for _, j := range f.byDigest[argDig] {
			f.promote(j, noSpan)
		}
		if argCall := f.db.Calls[argDig]; argCall != nil {
			pushArgs(argCall)
		}
	}
}
