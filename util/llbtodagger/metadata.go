package llbtodagger

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
)

func applyDockerImageConfig(id *call.ID, img *dockerspec.DockerOCIImage) (*call.ID, error) {
	if img == nil {
		return id, nil
	}

	cfg := img.Config
	if cfg.ArgsEscaped && strings.EqualFold(img.OS, "windows") {
		return nil, fmt.Errorf("llbtodagger: unsupported image config argsEscaped on Windows image")
	}

	ctrID := id

	if cfg.User != "" {
		ctrID = appendCall(ctrID, containerType(), "withUser", argString("name", cfg.User))
	}
	if cfg.WorkingDir != "" {
		ctrID = appendCall(ctrID, containerType(), "withWorkdir", argString("path", cfg.WorkingDir))
	}

	for _, envKV := range cfg.Env {
		name, val, ok := strings.Cut(envKV, "=")
		if !ok {
			return nil, fmt.Errorf("llbtodagger: invalid image config env entry %q", envKV)
		}
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withEnvVariable",
			argString("name", name),
			argString("value", val),
		)
	}

	labelKeys := sortedMapKeys(cfg.Labels)
	for _, key := range labelKeys {
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withLabel",
			argString("name", key),
			argString("value", cfg.Labels[key]),
		)
	}

	exposedPorts := sortedMapKeys(cfg.ExposedPorts)
	for _, raw := range exposedPorts {
		port, proto, err := parseExposedPort(raw)
		if err != nil {
			return nil, fmt.Errorf("llbtodagger: invalid exposed port %q: %w", raw, err)
		}
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withExposedPort",
			argInt("port", int64(port)),
			argEnum("protocol", proto),
		)
	}

	if cfg.Entrypoint == nil {
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withoutEntrypoint",
			argBool("keepDefaultArgs", true),
		)
	} else {
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withEntrypoint",
			argStringList("args", cfg.Entrypoint),
			argBool("keepDefaultArgs", true),
		)
	}

	if cfg.Cmd == nil {
		ctrID = appendCall(ctrID, containerType(), "withoutDefaultArgs")
	} else {
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withDefaultArgs",
			argStringList("args", cfg.Cmd),
		)
	}

	metadataArgs := []*call.Argument{}
	if cfg.Healthcheck != nil {
		healthcheckJSON, err := json.Marshal(cfg.Healthcheck)
		if err != nil {
			return nil, fmt.Errorf("llbtodagger: marshal image config healthcheck: %w", err)
		}
		metadataArgs = append(metadataArgs, argString("healthcheck", string(healthcheckJSON)))
	}
	if len(cfg.OnBuild) > 0 {
		metadataArgs = append(metadataArgs, argStringList("onBuild", cfg.OnBuild))
	}
	if len(cfg.Shell) > 0 {
		metadataArgs = append(metadataArgs, argStringList("shell", cfg.Shell))
	}
	if len(cfg.Volumes) > 0 {
		metadataArgs = append(metadataArgs, argStringList("volumes", sortedMapKeys(cfg.Volumes)))
	}
	if cfg.StopSignal != "" {
		metadataArgs = append(metadataArgs, argString("stopSignal", cfg.StopSignal))
	}
	if len(metadataArgs) > 0 {
		ctrID = appendCall(ctrID, containerType(), "__withImageConfigMetadata", metadataArgs...)
	}

	return ctrID, nil
}

func parseExposedPort(raw string) (int, string, error) {
	portStr, proto, ok := strings.Cut(raw, "/")
	if !ok {
		portStr = raw
		proto = "tcp"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, "", err
	}
	if port <= 0 || port > 65535 {
		return 0, "", fmt.Errorf("port %d out of range", port)
	}

	switch strings.ToLower(proto) {
	case "tcp", "":
		return port, "TCP", nil
	case "udp":
		return port, "UDP", nil
	default:
		return 0, "", fmt.Errorf("unsupported protocol %q", proto)
	}
}

func sortedMapKeys[T any](m map[string]T) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
