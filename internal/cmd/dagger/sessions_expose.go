package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/sessionwire"
	"github.com/spf13/cobra"
)

type exposeUsageError struct{ error }

type exposePreparation struct {
	Startup *exposeStartup
	Request exposeRequest
	Paths   exposePaths
}

func newSessionsExposeCommand() *cobra.Command {
	var (
		portSpecs []string
		replace   bool
		stop      bool
	)
	cmd := &cobra.Command{
		Use:   "expose SESSION",
		Short: "Make a detached up session's service ports available on this machine",
		Long: `Make the session's service ports available on this machine.

Idempotent: with no flags, or with the same normalized port request the live
server already serves, prints the current addresses and exits. A different
port request against a live server errors; use --replace.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSessionCLIArg(cmd, args[0]); err != nil {
				return err
			}
			if stop && (replace || len(portSpecs) > 0) {
				return exposeCommandUsageError(cmd, errors.New("--stop cannot be combined with --replace or --port"))
			}
			if !exposePlatformSupported() {
				return errors.New("detached up and sessions expose are not yet supported on Windows")
			}
			err := exposeDetachableSession(cmd, args[0], portSpecs, replace, stop)
			var usageErr *exposeUsageError
			if errors.As(err, &usageErr) {
				return exposeCommandUsageError(cmd, usageErr.error)
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&portSpecs, "port", nil, "Add or override SERVICE=[FRONTEND]:BACKEND[/PROTO]")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace a live local port server with this port set")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop the live local port server without terminating the session")
	return cmd
}

func exposeCommandUsageError(cmd *cobra.Command, err error) error {
	cmd.PrintErrln(cmd.ErrPrefix(), err)
	cmd.PrintErrf("Run '%s --help' for usage.\n", cmd.CommandPath())
	return idtui.ExitError{OriginalCode: 2, Original: err}
}

func exposeDetachableSession(
	cmd *cobra.Command,
	sessionID string,
	portSpecs []string,
	replace bool,
	stop bool,
) error {
	return withSessionControlClient(cmd.Context(), func(ctx context.Context, control *client.ControlClient) error {
		descriptor, err := control.InspectSession(ctx, sessionID)
		if err != nil {
			return err
		}
		stateDir, err := exposeStateDirectory()
		if err != nil {
			return err
		}
		paths, err := makeExposePaths(stateDir, sessionID)
		if err != nil {
			return err
		}
		if stop {
			if err := stopLocalExpose(ctx, paths); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Stopped local ports for %s\n", sessionID)
			return err
		}
		if descriptor.Query == nil || descriptor.Query.Presentation.Kind != "up" {
			return errors.New("session has no up services")
		}
		if descriptor.Query.Status == engine.SessionQueryStateRunning {
			fmt.Fprintln(cmd.ErrOrStderr(), "Services are still starting...")
		}
		query, err := waitForPrimaryQuery(ctx, func(ctx context.Context) (engine.SessionQuery, error) {
			return control.InspectPrimaryQuery(ctx, sessionID)
		})
		if err != nil {
			return err
		}
		result, err := control.PrimaryQueryResult(ctx, sessionID)
		if err != nil {
			return err
		}
		value, err := decodeDetachedResult(result.Body, query.Presentation)
		if err != nil {
			return err
		}
		upResult, ok := value.(sessionwire.DetachedUpResult)
		if !ok {
			return fmt.Errorf("saved up result is %T", value)
		}
		request, err := normalizeExposeRequest(upResult, portSpecs)
		if err != nil {
			return err
		}
		descriptor, err = control.InspectSession(ctx, sessionID)
		if err != nil {
			return err
		}
		remoteHolder := ""
		if descriptor.Attachment != nil && sessionPortCount(descriptor) > 0 {
			remoteHolder = descriptor.Attachment.Hostname
			if remoteHolder == "" {
				remoteHolder = descriptor.Attachment.ClientID
			}
		}

		preparation, err := prepareExposePorts(ctx, sessionID, stateDir, request, len(portSpecs) == 0, replace, remoteHolder, func(message string) {
			fmt.Fprintln(cmd.ErrOrStderr(), message)
		})
		if err != nil {
			return err
		}
		committed := false
		if preparation.Startup != nil {
			defer func() {
				if !committed {
					_ = preparation.Startup.Abort()
				}
			}()
			if err := preparation.Startup.Commit(ctx); err != nil {
				return err
			}
			committed = true
			return writeExposeSummary(cmd.OutOrStdout(), sessionID, preparation.Startup.Ports, preparation.Paths.Log)
		}

		descriptor, err = control.InspectSession(ctx, sessionID)
		if err != nil {
			return err
		}
		ports := exposedPortsFromDescriptor(descriptor, preparation.Request)
		return writeExposeSummary(cmd.OutOrStdout(), sessionID, ports, preparation.Paths.Log)
	})
}

func prepareExposePorts(
	ctx context.Context,
	sessionID string,
	stateDir string,
	request exposeRequest,
	noExplicitPorts bool,
	replace bool,
	remoteHolder string,
	notify func(string),
) (exposePreparation, error) {
	paths, err := makeExposePaths(stateDir, sessionID)
	if err != nil {
		return exposePreparation{}, err
	}
	reportedStarting := false
	for {
		lock, status, err := inspectLocalExpose(ctx, paths)
		if err != nil {
			return exposePreparation{}, err
		}
		if lock != nil {
			if remoteHolder != "" {
				_ = lock.Close()
				return exposePreparation{}, fmt.Errorf(
					"ports are being served from %s; see dagger sessions inspect %s",
					remoteHolder, sessionID,
				)
			}
			if notify != nil {
				notify("Publishing ports...")
			}
			startup, err := spawnExposeServer(ctx, exposeServerConfig{
				SessionID: sessionID, StateDir: stateDir, Request: request,
			}, paths, lock)
			if err != nil {
				return exposePreparation{}, err
			}
			return exposePreparation{Startup: startup, Request: request, Paths: paths}, nil
		}
		if status.State == exposeStateStarting && !replace {
			if !reportedStarting && notify != nil {
				notify("Local port server is starting...")
				reportedStarting = true
			}
			if err := waitExposeRetry(ctx); err != nil {
				return exposePreparation{}, err
			}
			continue
		}
		if status.State == exposeStateReady && !replace && (noExplicitPorts || request.equal(status.Request)) {
			return exposePreparation{Request: status.Request, Paths: paths}, nil
		}
		if status.State == exposeStateReady && !replace {
			return exposePreparation{}, errors.New("ports are already served with a different port set; use --replace")
		}
		lock, err = stopAndAcquireLocalExpose(ctx, paths)
		if err != nil {
			return exposePreparation{}, err
		}
		if notify != nil {
			notify("Publishing ports...")
		}
		startup, err := spawnExposeServer(ctx, exposeServerConfig{
			SessionID: sessionID, StateDir: stateDir, Request: request,
		}, paths, lock)
		if err != nil {
			return exposePreparation{}, err
		}
		return exposePreparation{Startup: startup, Request: request, Paths: paths}, nil
	}
}

func parseExposePortSpec(spec string) (exposePortMapping, error) {
	service, mapping, ok := strings.Cut(spec, "=")
	if !ok || service == "" || mapping == "" {
		return exposePortMapping{}, &exposeUsageError{fmt.Errorf(
			"invalid --port %q: expected SERVICE=[FRONTEND]:BACKEND[/PROTO]", spec,
		)}
	}
	portPart, protocolPart, hasProtocol := strings.Cut(mapping, "/")
	if strings.Contains(protocolPart, "/") {
		hasProtocol = true
		protocolPart = "invalid"
	}
	protocol := sessionwire.NetworkProtocolTCP
	if hasProtocol {
		protocol = sessionwire.NetworkProtocol(strings.ToUpper(protocolPart))
		if protocol != sessionwire.NetworkProtocolTCP && protocol != sessionwire.NetworkProtocolUDP {
			return exposePortMapping{}, &exposeUsageError{fmt.Errorf("invalid protocol %q in --port %q", protocolPart, spec)}
		}
	}
	frontendPart, backendPart, ok := strings.Cut(portPart, ":")
	if !ok || backendPart == "" {
		return exposePortMapping{}, &exposeUsageError{fmt.Errorf(
			"invalid --port %q: expected SERVICE=[FRONTEND]:BACKEND[/PROTO]", spec,
		)}
	}
	backend, err := parseExposePortNumber("backend", backendPart, spec)
	if err != nil {
		return exposePortMapping{}, err
	}
	var frontend *int
	if frontendPart != "" {
		value, err := parseExposePortNumber("frontend", frontendPart, spec)
		if err != nil {
			return exposePortMapping{}, err
		}
		frontend = &value
	}
	return exposePortMapping{
		Service: service, Frontend: frontend, Backend: backend, Protocol: protocol,
	}, nil
}

func parseExposePortNumber(kind, value, spec string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, &exposeUsageError{fmt.Errorf(
			"invalid %s port %q in --port %q: must be in 1..65535", kind, value, spec,
		)}
	}
	return port, nil
}

func normalizeExposeRequest(result sessionwire.DetachedUpResult, specs []string) (exposeRequest, error) {
	services := make(map[string]sessionwire.DetachedUpService, len(result.Services))
	mappings := map[string]exposePortMapping{}
	order := make([]string, 0)
	put := func(mapping exposePortMapping) {
		key := fmt.Sprintf("%s\x00%d\x00%s", mapping.Service, mapping.Backend, mapping.Protocol)
		if _, exists := mappings[key]; !exists {
			order = append(order, key)
		}
		mappings[key] = mapping
	}
	for _, service := range result.Services {
		services[service.Name] = service
		if service.Native {
			for _, port := range service.BackendPorts {
				protocol := port.Protocol
				if protocol == "" {
					protocol = sessionwire.NetworkProtocolTCP
				}
				frontend := port.Port
				put(exposePortMapping{
					Service: service.Name, ServiceID: service.ServiceID, Frontend: &frontend,
					Backend: port.Port, Protocol: protocol,
				})
			}
		} else {
			for _, port := range service.PortMappings {
				protocol := port.Protocol
				if protocol == "" {
					protocol = sessionwire.NetworkProtocolTCP
				}
				put(exposePortMapping{
					Service: service.Name, ServiceID: service.ServiceID, Frontend: port.Frontend,
					Backend: port.Backend, Protocol: protocol,
				})
			}
		}
	}
	for _, spec := range specs {
		mapping, err := parseExposePortSpec(spec)
		if err != nil {
			return exposeRequest{}, err
		}
		service, ok := services[mapping.Service]
		if !ok {
			return exposeRequest{}, &exposeUsageError{fmt.Errorf("unknown up service %q in --port", mapping.Service)}
		}
		mapping.ServiceID = service.ServiceID
		put(mapping)
	}

	request := exposeRequest{Mappings: make([]exposePortMapping, 0, len(mappings))}
	fixed := map[string]exposePortMapping{}
	for _, key := range order {
		mapping := mappings[key]
		if mapping.Protocol == sessionwire.NetworkProtocolUDP {
			return exposeRequest{}, &exposeUsageError{fmt.Errorf(
				"UDP port forwarding is not supported (%s:%d/udp)", mapping.Service, mapping.Backend,
			)}
		}
		if mapping.Protocol != sessionwire.NetworkProtocolTCP {
			return exposeRequest{}, fmt.Errorf("unsupported saved network protocol %q", mapping.Protocol)
		}
		if mapping.Frontend != nil {
			collisionKey := fmt.Sprintf("%d\x00%s", *mapping.Frontend, mapping.Protocol)
			if previous, exists := fixed[collisionKey]; exists {
				return exposeRequest{}, &exposeUsageError{fmt.Errorf(
					"port %d/%s is requested by both %s and %s",
					*mapping.Frontend, strings.ToLower(string(mapping.Protocol)), previous.Service, mapping.Service,
				)}
			}
			fixed[collisionKey] = mapping
		}
		request.Mappings = append(request.Mappings, mapping)
	}
	if len(request.Mappings) == 0 {
		return exposeRequest{}, errors.New("session has no up service ports")
	}
	return request.canonical(), nil
}

func exposedPortsFromDescriptor(descriptor engine.SessionDescriptor, request exposeRequest) []exposedPort {
	namesByKey := map[engine.SessionServiceKey][]string{}
	for _, service := range descriptor.Services {
		if service.Retained && len(service.Names) > 0 {
			namesByKey[service.Key] = append([]string(nil), service.Names...)
		}
	}
	used := make([]bool, len(request.Mappings))
	ports := make([]exposedPort, 0)
	for _, service := range descriptor.Services {
		if service.TunnelUpstream == nil {
			continue
		}
		aliases := namesByKey[*service.TunnelUpstream]
		for _, port := range service.Ports {
			protocol := sessionwire.NetworkProtocol(strings.ToUpper(port.Protocol))
			mappingIndex := -1
			for i, mapping := range request.Mappings {
				if used[i] || mapping.Protocol != protocol || !containsString(aliases, mapping.Service) {
					continue
				}
				if mapping.Frontend != nil && *mapping.Frontend == port.Port {
					mappingIndex = i
					break
				}
				if mapping.Frontend == nil && mappingIndex == -1 {
					mappingIndex = i
				}
			}
			published := exposedPort{Frontend: port.Port, Backend: port.Port, Protocol: protocol}
			if len(aliases) > 0 {
				published.Service = aliases[0]
			}
			if mappingIndex >= 0 {
				mapping := request.Mappings[mappingIndex]
				used[mappingIndex] = true
				published.Service = mapping.Service
				published.Backend = mapping.Backend
			}
			ports = append(ports, published)
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Service != ports[j].Service {
			return ports[i].Service < ports[j].Service
		}
		return ports[i].Frontend < ports[j].Frontend
	})
	return ports
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func writeExposeSummary(w io.Writer, sessionID string, ports []exposedPort, logPath string) error {
	if _, err := fmt.Fprintf(w, "Ports for detached session %s\n", sessionID); err != nil {
		return err
	}
	if formatted := formatExposedPorts(ports); formatted != "" {
		if _, err := fmt.Fprintln(w, formatted); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "Ports served by a background process on this machine (log: %s)\n", logPath)
	return err
}
