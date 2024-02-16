package telemetry

import (
	"github.com/vito/progrock"
	"google.golang.org/protobuf/proto"
)

type LegacyIDInternalizer struct {
	w progrock.Writer
}

func NewLegacyIDInternalizer(w progrock.Writer) LegacyIDInternalizer {
	return LegacyIDInternalizer{w: w}
}

var _ progrock.Writer = LegacyIDInternalizer{}

// WriteStatus marks any vertexes with a label "id" as internal so that they
// are hidden from interfaces that predate Zenith.
func (f LegacyIDInternalizer) WriteStatus(status *progrock.StatusUpdate) error {
	var foundIds []int
	for i, v := range status.Vertexes {
		for _, l := range v.Labels {
			if l.Name == "id" && l.Value == "true" {
				foundIds = append(foundIds, i)
			}
		}
	}
	if len(foundIds) == 0 {
		// avoid a full copy in the common case
		return f.w.WriteStatus(status)
	}
	downstream := proto.Clone(status).(*progrock.StatusUpdate)
	for _, i := range foundIds {
		downstream.Vertexes[i].Internal = true
	}
	return f.w.WriteStatus(downstream)
}

func (f LegacyIDInternalizer) Close() error {
	return f.w.Close()
}
