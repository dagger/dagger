package dagql

import (
	"fmt"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/util/hashutil"
)

// ExtraDigestLabelPlatform labels the identity of a result declared as made
// for a platform (PlatformDigest).
const ExtraDigestLabelPlatform = "platform"

// buildPlatformPlaceholder stands, in a declared result's identity, for the
// platform its commands ran on.
const buildPlatformPlaceholder = "<build platform>"

// BuildPlatform is where a recipe names the platform its commands ran on,
// which a declared result's identity leaves out.
type BuildPlatform struct {
	// ImplicitInput names the implicit input that gives a call the platform
	// of the engine that made it.
	ImplicitInput string
	// IsPlatformArg reports whether field of receiverType takes arg as a
	// platform.
	IsPlatformArg func(receiverType, field string, view call.View, arg string) bool
	// IsCommand reports whether field of receiverType runs a command in its
	// receiver, as Container.withExec does.
	IsCommand func(receiverType, field string) bool
}

// PlatformDigest is the identity of the result of frame declared as made for
// platform: its recipe with the platform its commands ran on left out, where
// build says a recipe names it. A declaration vouches that the result's content doesn't depend on
// that platform, so engines of different platforms that make it for the same
// platform, each running its own platform's tools, give it the same
// identity, and each finds the other's.
//
// It hashes the recipe as its recipe digest does, with every input by this
// same identity. The platform a command ran on is its container's: on the
// container a command runs in (build.IsCommand), from that command back to
// where the container starts, every platform a call takes is replaced, and
// content digests, which name that platform when a call makes them from its
// recipe, as Container.from's does, are left out. Elsewhere a platform is a
// choice of input, such as the sysroot of an image pulled for the target,
// and is kept, and an input's content digest stands for it, as in its
// content-preferred digest. The engine-default-platform input names the
// engine that made the call, not a choice, and is always replaced: an input
// can be another engine's, such as an equivalent result an engine image
// shipped. An input itself declared for a platform is identified by its own
// declaration.
//
// An input's content digest counts only once it is known: a declaration
// made before an input learns its content is identified by the input's
// recipe, and doesn't find one made after. A declaration can take the later
// identity too by computing this again once its inputs are evaluated, as
// Directory.withPlatform's result does when it is evaluated.
func (c *Cache) PlatformDigest(frame *ResultCall, platform string, build BuildPlatform) (digest.Digest, error) {
	walk := &platformDigestWalk{cache: c, build: build, frames: map[platformDigestKey]digest.Digest{}, results: map[uint64]*ResultCall{}}
	dig, err := walk.digest(frame, false)
	if err != nil {
		return "", err
	}
	return hashutil.HashStrings("platform", platform, dig.String()), nil
}

type platformDigestKey struct {
	frame *ResultCall
	// command marks a frame of the container a command runs in.
	command bool
}

type platformDigestWalk struct {
	cache   *Cache
	build   BuildPlatform
	frames  map[platformDigestKey]digest.Digest
	results map[uint64]*ResultCall
}

func (walk *platformDigestWalk) digest(frame *ResultCall, command bool) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}
	key := platformDigestKey{frame, command}
	if dig, ok := walk.frames[key]; ok {
		if dig == "" {
			return "", fmt.Errorf("cycle while computing a platform digest")
		}
		return dig, nil
	}
	walk.frames[key] = ""
	for _, extra := range frame.ExtraDigests {
		if extra.Label == ExtraDigestLabelPlatform && extra.Digest != "" {
			walk.frames[key] = extra.Digest
			return extra.Digest, nil
		}
	}
	if content := frame.ContentDigest(); content != "" && !command {
		walk.frames[key] = content
		return content, nil
	}
	receiverType := "Query"
	if frame.Receiver != nil {
		receiver, err := walk.frame(frame.Receiver)
		if err != nil {
			return "", fmt.Errorf("receiver: %w", err)
		}
		if receiver.Type != nil {
			receiverType = receiver.Type.NamedType
		}
	}
	runs := frame.Kind == ResultCallKindField && walk.build.IsCommand != nil && walk.build.IsCommand(receiverType, frame.Field)
	args := frame.Args
	if command {
		args = walk.withoutBuildPlatform(args, func(arg string) bool {
			return frame.Kind == ResultCallKindField && walk.build.IsPlatformArg != nil &&
				walk.build.IsPlatformArg(receiverType, frame.Field, frame.View, arg)
		})
	}
	shape := &ResultCall{
		Kind:        frame.Kind,
		Type:        frame.Type,
		Field:       frame.Field,
		SyntheticOp: frame.SyntheticOp,
		View:        frame.View,
		Nth:         frame.Nth,
		Receiver:    frame.Receiver,
		Module:      frame.Module,
		Args:        args,
		ImplicitInputs: walk.withoutBuildPlatform(frame.ImplicitInputs, func(arg string) bool {
			return arg == walk.build.ImplicitInput
		}),
	}
	// The receiver of a command, and of a call on the container it runs
	// in, is that container; every other input is an input.
	dig, err := shape.digestWithInputs(func(ref *ResultCallRef) (digest.Digest, error) {
		input, err := walk.frame(ref)
		if err != nil {
			return "", err
		}
		return walk.digest(input, ref == frame.Receiver && (command || runs))
	})
	if err != nil {
		return "", err
	}
	walk.frames[key] = dig
	return dig, nil
}

// withoutBuildPlatform replaces the platform in the args isPlatform names.
func (walk *platformDigestWalk) withoutBuildPlatform(args []*ResultCallArg, isPlatform func(string) bool) []*ResultCallArg {
	var out []*ResultCallArg
	for i, arg := range args {
		if arg == nil || arg.Value == nil || arg.Value.Kind != ResultCallLiteralKindString || !isPlatform(arg.Name) {
			continue
		}
		if out == nil {
			out = append([]*ResultCallArg(nil), args...)
		}
		out[i] = &ResultCallArg{Name: arg.Name, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: buildPlatformPlaceholder}}
	}
	if out == nil {
		return args
	}
	return out
}

func (walk *platformDigestWalk) frame(ref *ResultCallRef) (*ResultCall, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Call != nil {
		return ref.Call, nil
	}
	if frame := walk.results[ref.ResultID]; frame != nil {
		return frame, nil
	}
	frame := ref.loadSharedCall()
	if frame == nil && walk.cache != nil {
		frame = walk.cache.resultCallByResultID(sharedResultID(ref.ResultID))
	}
	if frame == nil {
		return nil, fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
	}
	walk.results[ref.ResultID] = frame
	return frame, nil
}
