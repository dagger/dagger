package daggercmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/hotline"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
)

type hotlineCLI struct {
	json        bool
	demo        string
	mockFile    string
	org         string
	requestID   string
	draft       string
	out, errOut io.Writer
	in          io.Reader
}

func init() { rootCmd.AddCommand(newHotlineCmd()) }

func newHotlineCmd() *cobra.Command {
	a := &hotlineCLI{}
	h := a.command()
	h.GroupID = "toolbox"
	// Hotline works without a workspace or an engine, including in mock mode.
	h.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { cmd.SilenceUsage = true; return nil }
	bindIO := func(cmd *cobra.Command) {
		a.in, a.out, a.errOut = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	}
	h.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { bindIO(cmd); return a.failure(err) })
	var wrap func(*cobra.Command)
	wrap = func(cmd *cobra.Command) {
		if run := cmd.RunE; run != nil {
			cmd.RunE = func(cmd *cobra.Command, args []string) error { bindIO(cmd); return a.failure(run(cmd, args)) }
		}
		if args := cmd.Args; args != nil {
			cmd.Args = func(cmd *cobra.Command, values []string) error { bindIO(cmd); return a.failure(args(cmd, values)) }
		}
		for _, child := range cmd.Commands() {
			wrap(child)
		}
	}
	wrap(h)
	return h
}

func (a *hotlineCLI) failure(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *hotline.Error
	code, status := "invalid_input", 2
	if errors.As(err, &apiErr) {
		code, status = apiErr.Code, 1
		if apiErr.Status == 401 || apiErr.Status == 403 {
			status = 3
		}
	}
	if a.json {
		_ = json.NewEncoder(a.errOut).Encode(map[string]any{"error": map[string]any{"code": code, "message": err.Error(), "request_id": a.requestID, "draft": a.draft}})
	} else {
		fmt.Fprintln(a.errOut, "Error:", clean(err.Error()))
		if a.draft != "" {
			fmt.Fprintln(a.errOut, "Draft saved:", clean(a.draft))
		}
		if a.requestID != "" {
			fmt.Fprintln(a.errOut, "Retry with --request-id", clean(a.requestID))
		}
	}
	return idtui.ExitError{OriginalCode: status}
}

func (a *hotlineCLI) command() *cobra.Command {
	h := &cobra.Command{
		Use: "hotline", Short: "Exchange messages with the Dagger team",
		Long: `Exchange messages with the Dagger team. A Dagger Cloud account is required.
Use dagger login to sign in. Ask early when you need help.

Messages and attachments are shared with your organization and the Dagger team.
Use inbox to find unread messages. Use --json for machine output.
Commands do not prompt. An editor opens only with --editor.`,
		Args: cobra.NoArgs,
	}
	h.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	h.PersistentFlags().StringVar(&a.mockFile, "mock", os.Getenv("DAGGER_HOTLINE_MOCK"), "Store demo data in this JSON file; use no service")
	h.PersistentFlags().BoolVar(&a.json, "json", false, "Write JSON output")
	h.PersistentFlags().StringVar(&a.demo, "mock-user", "", "Mock account: member, admin, team, or other (requires --mock)")
	h.PersistentFlags().StringVar(&a.org, "org", "", "Cloud organization name (defaults to the current organization)")
	h.AddCommand(a.messageCommand(false), a.messageCommand(true), a.listCommand("list"), a.listCommand("inbox"), a.listCommand("queue"), a.readCommand(), a.watchCommand(), a.attachmentsCommand())
	return h
}

func (a *hotlineCLI) messageCommand(reply bool) *cobra.Command {
	var body, file, key, workspace string
	var paths []string
	cmd := &cobra.Command{Use: "new", Short: "Start a thread with the Dagger team", Args: cobra.NoArgs}
	if reply {
		cmd.Use = "reply THREAD"
		cmd.Short = "Add a message to a thread"
		cmd.Args = cobra.ExactArgs(1)
	} else {
		cwd, _ := os.Getwd()
		cmd.Flags().StringVar(&workspace, "workspace", filepath.Base(cwd), "Workspace name; only --attach selects files")
	}
	messageFlags(cmd, &body, &file, &key)
	cmd.Flags().Bool("editor", false, "Write the message in VISUAL or EDITOR (default: vi)")
	cmd.Flags().StringArrayVar(&paths, "attach", nil, "Attach this workspace file; repeat for each file")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		path := "/v1/hotline/threads"
		if reply {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			path = fmt.Sprintf("%s/%d/messages", path, id)
		}
		text, err := a.body(cmd, body, file)
		if err != nil {
			return err
		}
		files, err := collectFiles(paths)
		if err != nil {
			return err
		}
		input := hotline.Input{Body: text, Workspace: workspace, Attachments: files}
		if err = input.Validate(!reply); err != nil {
			return err
		}
		if err = a.send(cmd, path, key, input); err != nil {
			return err
		}
		if a.draft != "" {
			os.Remove(a.draft)
			a.draft = ""
		}
		return nil
	}
	return cmd
}

func messageFlags(cmd *cobra.Command, body, file, key *string) {
	cmd.Flags().StringVar(body, "body", "", "Message text")
	cmd.Flags().StringVar(file, "body-file", "", "Read message from a file; use - for standard input")
	cmd.Flags().StringVar(key, "request-id", "", "Reuse this ID when you retry the same message")
}

func (a *hotlineCLI) body(cmd *cobra.Command, body, file string) (string, error) {
	editor, _ := cmd.Flags().GetBool("editor")
	sources := 0
	for _, set := range []bool{cmd.Flags().Changed("body"), cmd.Flags().Changed("body-file"), editor} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return "", fmt.Errorf("Supply exactly one of --body, --body-file, or --editor.")
	}
	if editor {
		f, err := os.CreateTemp("", "dagger-hotline-*.md")
		if err != nil {
			return "", err
		}
		a.draft = f.Name()
		if err = f.Close(); err != nil {
			return "", err
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
		}
		argv, err := shlex.Split(editor)
		if err != nil || len(argv) == 0 {
			return "", fmt.Errorf("Set VISUAL or EDITOR to an editor command.")
		}
		edit := exec.CommandContext(cmd.Context(), argv[0], append(argv[1:], a.draft)...)
		edit.Stdin, edit.Stdout, edit.Stderr = a.in, a.errOut, a.errOut
		if err = edit.Run(); err != nil {
			return "", fmt.Errorf("Editor failed: %w", err)
		}
		file = a.draft
	}
	if file == "" && cmd.Flags().Changed("body-file") {
		return "", fmt.Errorf("Supply a file path, or - for standard input.")
	}
	if file == "" {
		return body, nil
	}
	r := a.in
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("Cannot open the message file.")
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, hotline.MaxBody+1))
	if err != nil {
		return "", fmt.Errorf("Cannot read the message.")
	}
	return string(b), nil
}

func (a *hotlineCLI) send(cmd *cobra.Command, path, key string, input hotline.Input) error {
	if key == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return err
		}
		key = hex.EncodeToString(b[:])
	}
	a.requestID = key
	var sent hotline.Sent
	if err := a.request(cmd, http.MethodPost, path, input, key, &sent); err != nil {
		return err
	}
	if a.json {
		return json.NewEncoder(a.out).Encode(sent)
	}
	_, err := fmt.Fprintf(a.out, "Message %d saved in thread %d.\nRequest ID: %s\n", sent.MessageID, sent.ThreadID, clean(sent.RequestID))
	return err
}

func (a *hotlineCLI) request(cmd *cobra.Command, method, path string, input any, key string, result any) error {
	if a.demo != "" && a.mockFile == "" {
		return fmt.Errorf("--mock-user requires --mock.")
	}
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()
	var res *http.Response
	var err error
	if a.mockFile != "" {
		req, reqErr := http.NewRequestWithContext(ctx, method, "http://mock.local"+path, body)
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Idempotency-Key", key)
		res, err = hotline.MockRequest(req, a.mockFile, a.demo)
	} else {
		client, credentials, authErr := cloudCLI.cloudClientWithLogin(ctx, false)
		if authErr != nil {
			return &hotline.Error{Status: 401, Code: "unauthenticated", Message: "Sign in with dagger login before using Hotline."}
		}
		orgID := ""
		if credentials.Org != nil {
			orgID = credentials.Org.ID
		}
		needsOrg := strings.SplitN(path, "?", 2)[0] == "/v1/hotline/threads"
		if a.org != "" || (needsOrg && orgID == "" && credentials.Token.Type() != "Basic" && credentials.Token.Type() != "OIDC") {
			org, orgErr := cloudCLI.resolveCloudOrgName(ctx, client, credentials, a.org)
			if orgErr != nil {
				return &hotline.Error{Status: 403, Code: "forbidden", Message: "Cannot select the requested organization."}
			}
			orgID = org.ID
		}
		res, err = client.Hotline(ctx, orgID, method, path, key, body)
	}
	if err != nil {
		if a.mockFile != "" {
			return err
		}
		return &hotline.Error{Status: 503, Code: "unavailable", Message: "Cannot reach Hotline. Retry with the same request ID."}
	}
	defer res.Body.Close()
	d := json.NewDecoder(io.LimitReader(res.Body, 128*1024*1024))
	if res.StatusCode != http.StatusOK {
		var envelope struct {
			Error hotline.Error `json:"error"`
		}
		if d.Decode(&envelope) != nil || envelope.Error.Code == "" {
			return &hotline.Error{Status: res.StatusCode, Code: "api_error", Message: fmt.Sprintf("Hotline returned HTTP %d.", res.StatusCode)}
		}
		envelope.Error.Status = res.StatusCode
		return &envelope.Error
	}
	if bundle, ok := result.(*bytes.Buffer); ok {
		if res.Header.Get("Content-Type") != "application/zip" {
			return fmt.Errorf("Expected an attachment ZIP.")
		}
		n, err := io.Copy(bundle, io.LimitReader(res.Body, 2*hotline.MaxAttachmentBytes+1))
		if err != nil || n > 2*hotline.MaxAttachmentBytes {
			return fmt.Errorf("Cannot read the attachment bundle.")
		}
		return nil
	}
	if err = d.Decode(result); err != nil {
		return &hotline.Error{Status: 503, Code: "invalid_response", Message: "Invalid server response. Retry with the same request ID."}
	}
	return nil
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("Use a positive ID.")
	}
	return id, nil
}

func clean(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
}
