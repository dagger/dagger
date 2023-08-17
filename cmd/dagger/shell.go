package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/gorilla/websocket"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(shellCmd)
}

var shellCmd = &cobra.Command{
	Use:                "shell",
	DisableFlagParsing: true,
	Hidden:             true, // for now, remove once we're ready for primetime
	RunE:               loadEnvCmdWrapper(RunShell),
}

var (
	// TODO:dedupe w/ same thing in core
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

func RunShell(
	ctx context.Context,
	engineClient *client.Client,
	c *dagger.Client,
	env *dagger.Environment,
	cmd *cobra.Command,
	dynamicCmdArgs []string,
) error {
	if len(dynamicCmdArgs) == 0 {
		// open a shell to the environment itself
		// TODO: not sure if this behavior is confusing, but it's definitely helpful for debugging atm
		shellEndpoint, err := env.Runtime().ShellEndpoint(ctx)
		if err != nil {
			return fmt.Errorf("failed to get shell endpoint: %w", err)
		}
		return attachToShell(ctx, engineClient, shellEndpoint)
	}

	envShells, err := env.Shells(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment shells: %w", err)
	}
	for _, envShell := range envShells {
		envShell := envShell
		subCmds, err := addShell(ctx, &envShell, c, engineClient)
		if err != nil {
			return fmt.Errorf("failed to add subcmd: %w", err)
		}
		cmd.AddCommand(subCmds...)
	}

	subShell, _, err := cmd.Find(dynamicCmdArgs)
	if err != nil {
		return fmt.Errorf("failed to find: %w", err)
	}

	// prevent errors below from double printing
	cmd.Root().SilenceErrors = true
	cmd.Root().SilenceUsage = true
	// If there's any overlaps between dagger cmd args and the dynamic cmd args
	// we want to ensure they are parsed separately. For some reason, this flag
	// does that ¯\_(ツ)_/¯
	cmd.Root().TraverseChildren = true
	// TODO(vito): workaround for above breaking when used with -e flag
	cmd.Root().SetArgs(append([]string{"shell"}, dynamicCmdArgs...))

	rec := progrock.RecorderFromContext(ctx)

	if subShell.Name() == cmd.Name() {
		cmd.Println(subShell.UsageString())
		return fmt.Errorf("entrypoint not found or not set")
	}
	rec.Debug("running command", progrock.Labelf("command", subShell.Name()))
	err = subShell.Execute()
	if err != nil {
		rec.Error("failed to execute command", progrock.ErrorLabel(err))
		return fmt.Errorf("failed to execute shell subcmd: %w", err)
	}
	return nil
}

func addShell(ctx context.Context, envShell *dagger.EnvironmentShell, c *dagger.Client, engineClient *client.Client) ([]*cobra.Command, error) {
	rec := progrock.RecorderFromContext(ctx)

	// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
	// internally be doing this so it's not needed explicitly
	envShellID, err := envShell.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get shell id: %w", err)
	}
	envShell = c.EnvironmentShell(dagger.EnvironmentShellOpts{ID: envShellID})

	envShellName, err := envShell.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get shell name: %w", err)
	}
	description, err := envShell.Description(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get shell description: %w", err)
	}

	envShellFlags, err := envShell.Flags(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get shell flags: %w", err)
	}

	cmdName := getCommandName(nil, envShellName)
	subcmd := &cobra.Command{
		Use:         cmdName,
		Short:       description,
		Annotations: map[string]string{},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			vtx := rec.Vertex(
				digest.Digest("shell-"+envShellName),
				"shell "+envShellName,
			)
			defer func() { vtx.Done(err) }()

			cmd.SetOut(vtx.Stdout())
			cmd.SetErr(vtx.Stderr())

			for _, flagName := range commandAnnotations(cmd.Annotations).getCommandSpecificFlags() {
				// skip help flag
				// TODO: doc that users can't name an args help
				if flagName == "help" {
					continue
				}
				flagVal, err := cmd.Flags().GetString(strcase.ToKebab(flagName))
				if err != nil {
					return fmt.Errorf("failed to get flag %q: %w", flagName, err)
				}
				envShell = envShell.SetStringFlag(flagName, flagVal)
			}

			shellEndpoint, err := envShell.Endpoint(ctx)
			if err != nil {
				return fmt.Errorf("failed to get shell endpoint: %w", err)
			}
			return attachToShell(ctx, engineClient, shellEndpoint)
		},
	}

	for _, flag := range envShellFlags {
		flagName, err := flag.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get flag name: %w", err)
		}
		flagDescription, err := flag.Description(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get flag description: %w", err)
		}
		subcmd.Flags().String(strcase.ToKebab(flagName), "", flagDescription)
		commandAnnotations(subcmd.Annotations).addCommandSpecificFlag(flagName)
	}
	returnCmds := []*cobra.Command{subcmd}
	subcmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		cmd.Printf("\nCommand %s - %s\n", cmdName, description)

		cmd.Printf("\nAvailable Subcommands:\n")
		maxNameLen := 0
		for _, subcmd := range returnCmds[1:] {
			nameLen := len(getCommandName(subcmd, ""))
			if nameLen > maxNameLen {
				maxNameLen = nameLen
			}
		}
		// we want to ensure the doc strings line up so they are readable
		spacing := strings.Repeat(" ", maxNameLen+2)
		for _, subcmd := range returnCmds[1:] {
			cmd.Printf("  %s%s%s\n", getCommandName(subcmd, ""), spacing[len(getCommandName(subcmd, "")):], subcmd.Short)
		}

		fmt.Printf("\nFlags:\n")
		maxFlagLen := 0
		var flags []*pflag.Flag
		cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name == "help" {
				return
			}
			flags = append(flags, flag)
			if len(flag.Name) > maxFlagLen {
				maxFlagLen = len(flag.Name)
			}
		})
		flagSpacing := strings.Repeat(" ", maxFlagLen+2)
		for _, flag := range flags {
			cmd.Printf("  --%s%s%s\n", flag.Name, flagSpacing[len(flag.Name):], flag.Usage)
		}
	})

	return returnCmds, nil
}

func attachToShell(ctx context.Context, engineClient *client.Client, shellEndpoint string) error {
	rec := progrock.RecorderFromContext(ctx)

	// TODO:
	// fmt.Fprintf(os.Stderr, "shell endpoint: %s\n", shellEndpoint)

	dialer := &websocket.Dialer{
		// TODO: need use DialNestedContext when, well, you know, nested. Fix in engine client
		NetDialContext: engineClient.DialContext,
		// TODO:
		// HandshakeTimeout: 60 * time.Second, // TODO: made up number
	}
	wsconn, _, err := dialer.DialContext(ctx, shellEndpoint, nil)
	if err != nil {
		return err
	}
	// wsconn is closed as part of the caller closing engineClient

	// TODO:
	// fmt.Fprintf(os.Stderr, "WE ARE SO CONNECTED\n")

	vtx := rec.Vertex("shell", "shell",
		progrock.Focused(),
		progrock.Zoomed(func(w, h int) error {
			message := append([]byte{}, resizePrefix...)
			message = append(message, []byte(fmt.Sprintf("%d;%d", w, h))...)
			return wsconn.WriteMessage(websocket.BinaryMessage, message)
		}))

	origState, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to get stdin state: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), origState)

	_, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(fmt.Sprintf("failed to set stdin to raw mode: %v", err))
	}

	// Handle incoming messages
	errCh := make(chan error)
	exitCode := 1
	go func() {
		defer close(errCh)

		for {
			_, buff, err := wsconn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read: %w", err)
				return
			}
			switch {
			case bytes.HasPrefix(buff, stdoutPrefix):
				vtx.Stdout().Write(bytes.TrimPrefix(buff, stdoutPrefix))
			case bytes.HasPrefix(buff, stderrPrefix):
				vtx.Stderr().Write(bytes.TrimPrefix(buff, stderrPrefix))
			case bytes.HasPrefix(buff, exitPrefix):
				code, err := strconv.Atoi(string(bytes.TrimPrefix(buff, exitPrefix)))
				if err == nil {
					exitCode = code
				}
			}
		}
	}()

	// Forward stdin to websockets
	go func() {
		for {
			b := make([]byte, 512)

			n, err := os.Stdin.Read(b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "read: %v\n", err)
				continue
			}
			message := append([]byte{}, stdinPrefix...)
			message = append(message, b[:n]...)
			err = wsconn.WriteMessage(websocket.BinaryMessage, message)
			if err != nil {
				fmt.Fprintf(os.Stderr, "write: %v\n", err)
				continue
			}
		}
	}()

	if err := <-errCh; err != nil {
		wsCloseErr := &websocket.CloseError{}
		if errors.As(err, &wsCloseErr) && wsCloseErr.Code == websocket.CloseNormalClosure {
			return nil
		}
		return fmt.Errorf("websocket close: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("exited with code %d", exitCode)
	}
	return nil
}
