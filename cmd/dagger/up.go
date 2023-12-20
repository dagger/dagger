package main

import (
	"fmt"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
)

var (
	portForwards       []string
	portForwardsNative bool
)

var upCmd = &FuncCommand{
	Name:  "up",
	Short: "Start a service and expose its ports to the host",
	Init: func(cmd *cobra.Command) {
		cmd.PersistentFlags().StringSliceVarP(&portForwards, "port", "p", nil, "Port forwarding rule in FRONTEND[:BACKEND][/PROTO] format.")
		cmd.PersistentFlags().BoolVarP(&portForwardsNative, "native", "n", false, "Forward all ports natively, i.e. match frontend port to backend.")
	},
	OnSelectObjectLeaf: func(c *FuncCommand, name string) error {
		switch name {
		case Service:
		case Container:
			c.Select("asService")
		default:
			return fmt.Errorf("up can only be called on a service or container, not %q", name)
		}
		c.Select("id")
		return nil
	},
	AfterResponse: func(c *FuncCommand, cmd *cobra.Command, returnType *modTypeDef, result any) error {
		srvID, ok := (result).(string)
		if !ok {
			return fmt.Errorf("unexpected type %T", result)
		}

		ctx := cmd.Context()

		srv := c.c.Dagger().LoadServiceFromID(dagger.ServiceID(srvID))

		opts := dagger.HostTunnelOpts{
			Native: portForwardsNative,
		}

		for _, f := range portForwards {
			pair, proto, ok := strings.Cut(f, "/")
			if !ok {
				proto = string(dagger.Tcp)
			}
			f, b, ok := strings.Cut(pair, ":")
			if !ok {
				b = f
			}
			frontend, err := strconv.Atoi(f)
			if err != nil {
				return fmt.Errorf("failed to parse frontend port: %w", err)
			}
			backend, err := strconv.Atoi(b)
			if err != nil {
				return fmt.Errorf("failed to parse backend port: %w", err)
			}
			opts.Ports = append(opts.Ports, dagger.PortForward{
				Frontend: frontend,
				Backend:  backend,
				Protocol: dagger.NetworkProtocol(proto),
			})
		}

		tunnel, err := c.c.Dagger().Host().Tunnel(srv, opts).Start(ctx)
		if err != nil {
			return fmt.Errorf("failed to start tunnel: %w", err)
		}

		ports, err := tunnel.Ports(ctx)
		if err != nil {
			return fmt.Errorf("failed to get ports: %w", err)
		}

		for _, port := range ports {
			num, err := port.Port(ctx)
			if err != nil {
				return fmt.Errorf("failed to get port: %w", err)
			}
			proto, err := port.Protocol(ctx)
			if err != nil {
				return fmt.Errorf("failed to get protocol: %w", err)
			}
			desc, err := port.Description(ctx)
			if err != nil {
				return fmt.Errorf("failed to get description: %w", err)
			}
			cmd.Printf("%d/%s: %s\n", num, proto, desc)
		}

		<-ctx.Done()

		return ctx.Err()
	},
}
