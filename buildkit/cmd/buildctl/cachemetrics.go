package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progresswriter"
	digest "github.com/opencontainers/go-digest"
)

type vtxInfo struct {
	cached    bool
	completed bool
	from      bool
	name      string
}

func tailVTXInfo(ctx context.Context, pw progresswriter.Writer, metricsCh <-chan *client.SolveStatus) map[digest.Digest]*vtxInfo {
	fromRegexp := regexp.MustCompile(`^\[.*\] FROM`)

	vtxMap := make(map[digest.Digest]*vtxInfo)
	for st := range metricsCh {
		for _, vtx := range st.Vertexes {
			if _, ok := vtxMap[vtx.Digest]; !ok {
				vtxMap[vtx.Digest] = &vtxInfo{
					name: vtx.Name,
				}
			}
			if fromRegexp.MatchString(vtx.Name) {
				vtxMap[vtx.Digest].from = true
			}
			if vtx.Cached {
				vtxMap[vtx.Digest].cached = true
			}
			if vtx.Completed != nil {
				vtxMap[vtx.Digest].completed = true
			}
		}
	}
	return vtxMap
}

func outputCacheMetrics(out *os.File, startTime time.Time, vtxMap map[digest.Digest]*vtxInfo) {
	metrics := struct {
		Total            int64 `json:"total"`
		Completed        int64 `json:"completed"`
		UserTotal        int64 `json:"user_total"`
		UserCached       int64 `json:"user_cached"`
		UserCompleted    int64 `json:"user_completed"`
		UserCacheable    int64 `json:"user_cacheable"`
		From             int64 `json:"from"`
		Miss             int64 `json:"miss"`
		ClientDurationMS int64 `json:"client_duration_ms"`
	}{
		ClientDurationMS: time.Since(startTime).Milliseconds(),
	}
	for _, vtx := range vtxMap {
		metrics.Total++
		if vtx.completed {
			metrics.Completed++
		}
		if strings.HasPrefix(vtx.name, "[internal]") ||
			strings.HasPrefix(vtx.name, "[auth]") ||
			strings.HasPrefix(vtx.name, "importing cache") ||
			strings.HasPrefix(vtx.name, "exporting ") {
			continue
		}
		metrics.UserTotal++
		metrics.UserCacheable++
		if vtx.cached {
			metrics.UserCached++
		} else if !vtx.from {
			metrics.Miss++
			fmt.Fprintf(out, "cache miss: %s\n", vtx.name)
		}
		if vtx.completed {
			metrics.UserCompleted++
		}
		if vtx.from {
			metrics.From++
			metrics.UserCacheable--
		}
	}
	dt, _ := json.Marshal(metrics)
	fmt.Fprintln(out, string(dt))
}
