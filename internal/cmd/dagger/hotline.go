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
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/hotline"
	"github.com/spf13/cobra"
)

type hotlineCLI struct {
	json        bool
	demo        string
	mockFile    string
	org         string
	requestID   string
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
		_ = json.NewEncoder(a.errOut).Encode(map[string]any{"error": map[string]any{"code": code, "message": err.Error(), "request_id": a.requestID}})
	} else {
		fmt.Fprintln(a.errOut, "Error:", clean(err.Error()))
		if a.requestID != "" {
			fmt.Fprintln(a.errOut, "Retry with --request-id", clean(a.requestID))
		}
	}
	return idtui.ExitError{OriginalCode: status}
}

func (a *hotlineCLI) command() *cobra.Command {
	h := &cobra.Command{
		Use: "hotline", Short: "Send a message to the Dagger team",
		Long: `Send a message to the Dagger team. A Dagger Cloud account is required. Use dagger login to sign in.

Ask early when you are blocked. You do not need a complete bug report.
Use setup to report the result of your first setup attempt.
Replies are asynchronous. Use list and read to check for replies.

Commands never open a browser, editor, or prompt. Use --json for agents.
Messages and the workspace name are shared with your organization and the team.
Billing threads are limited to organization admins and the team.`,
	}
	h.PersistentFlags().StringVar(&a.mockFile, "mock", os.Getenv("DAGGER_HOTLINE_MOCK"), "Store demo data in this JSON file; use no service")
	h.PersistentFlags().BoolVar(&a.json, "json", false, "Write JSON output")
	h.PersistentFlags().StringVar(&a.demo, "mock-user", "", "Mock account: member, admin, team, or other (requires --mock)")
	h.PersistentFlags().StringVar(&a.org, "org", "", "Cloud organization name (defaults to the current organization)")
	h.Args = cobra.NoArgs
	h.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
	descriptions := map[string]string{
		"setup":      "Report your first setup result: success, blocked, or stopped",
		"feedback":   "Report your experience with Dagger",
		"question":   "Ask a question about Dagger in this workspace",
		"bug":        "Report a defect; incomplete reports are welcome",
		"billing":    "Ask about billing (organization admins only)",
		"suggestion": "Suggest an improvement",
	}
	for _, kind := range hotline.Kinds {
		var body, file, key string
		var paths []string
		input := hotline.Input{Kind: kind}
		cmd := &cobra.Command{Use: kind, Short: descriptions[kind], Args: cobra.NoArgs}
		messageFlags(cmd, &body, &file, &key)
		cmd.Flags().StringArrayVar(&paths, "attach", nil, "Attach this workspace file; repeat for each file")
		cwd, _ := os.Getwd()
		cmd.Flags().StringVar(&input.Workspace, "workspace", filepath.Base(cwd), "Workspace name; no files are collected")
		if kind == "setup" {
			cmd.Flags().StringVar(&input.Outcome, "outcome", "", "Required: success, blocked, or stopped")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			var err error
			input.Body, err = a.body(cmd, body, file)
			if err != nil {
				return err
			}
			input.Attachments, err = collectFiles(paths)
			if err != nil {
				return err
			}
			if err = input.Validate(true); err != nil {
				return err
			}
			return a.send(cmd, "/v1/hotline/threads", key, input)
		}
		h.AddCommand(cmd)
	}
	list := &cobra.Command{Use: "list", Short: "List the latest 50 threads", Args: cobra.NoArgs}
	list.RunE = func(cmd *cobra.Command, args []string) error {
		var threads []hotline.Thread
		if err := a.request(cmd, http.MethodGet, "/v1/hotline/threads", nil, "", &threads); err != nil {
			return err
		}
		if a.json {
			return json.NewEncoder(a.out).Encode(threads)
		}
		if len(threads) == 0 {
			fmt.Fprintln(a.out, "No threads.")
		}
		for _, t := range threads {
			fmt.Fprintf(a.out, "%d\t%s\t%s\t%s\n", t.ID, t.Kind, clean(t.Workspace), t.CreatedAt.Format(time.RFC3339))
		}
		return nil
	}
	h.AddCommand(list)
	var after int64
	read := &cobra.Command{Use: "read THREAD", Short: "Read up to 100 messages; use --after with the last message ID for more", Args: cobra.ExactArgs(1)}
	read.Flags().Int64Var(&after, "after", 0, "Return messages after this message ID")
	read.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}
		if after < 0 {
			return fmt.Errorf("Use a nonnegative message ID for --after.")
		}
		var c hotline.Conversation
		if err = a.request(cmd, http.MethodGet, fmt.Sprintf("/v1/hotline/threads/%d?after=%d", id, after), nil, "", &c); err != nil {
			return err
		}
		if a.json {
			return json.NewEncoder(a.out).Encode(c)
		}
		fmt.Fprintf(a.out, "Thread %d · %s · %s\n", c.Thread.ID, c.Thread.Kind, clean(c.Thread.Workspace))
		if c.Thread.Outcome != "" {
			fmt.Fprintln(a.out, "Setup result:", c.Thread.Outcome)
		}
		for _, m := range c.Messages {
			who := clean(m.AuthorID)
			if m.Team {
				who += " (Dagger team)"
			}
			fmt.Fprintf(a.out, "\n[%d] %s · %s\n%s\n", m.ID, who, m.CreatedAt.Format(time.RFC3339), clean(m.Body))
			for _, f := range m.Attachments {
				fmt.Fprintf(a.out, "  Attached: %s (%d bytes)\n", clean(f.Path), f.Size)
			}
			if len(m.Attachments) > 0 {
				fmt.Fprintf(a.out, "  Download: dagger hotline attachments %d --output workspace.zip\n", m.ID)
			}
		}
		if len(c.Messages) == 100 {
			fmt.Fprintf(a.out, "\nContinue: dagger hotline read %d --after %d\n", id, c.Messages[99].ID)
		}
		return nil
	}
	h.AddCommand(read)
	var body, file, key string
	var paths []string
	reply := &cobra.Command{Use: "reply THREAD", Short: "Add a message to a thread", Args: cobra.ExactArgs(1)}
	messageFlags(reply, &body, &file, &key)
	reply.Flags().StringArrayVar(&paths, "attach", nil, "Attach this workspace file; repeat for each file")
	reply.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0])
		if err != nil {
			return err
		}
		body, err := a.body(cmd, body, file)
		if err != nil {
			return err
		}
		files, err := collectFiles(paths)
		if err != nil {
			return err
		}
		input := hotline.Input{Body: body, Attachments: files}
		if err = input.Validate(false); err != nil {
			return err
		}
		return a.send(cmd, fmt.Sprintf("/v1/hotline/threads/%d/messages", id), key, input)
	}
	h.AddCommand(reply)
	h.AddCommand(a.attachmentsCommand())
	return h
}

func messageFlags(cmd *cobra.Command, body, file, key *string) {
	cmd.Flags().StringVar(body, "body", "", "Message text")
	cmd.Flags().StringVar(file, "body-file", "", "Read message from a file; use - for standard input")
	cmd.Flags().StringVar(key, "request-id", "", "Reuse this ID when you retry the same message")
}

func (a *hotlineCLI) body(cmd *cobra.Command, body, file string) (string, error) {
	if cmd.Flags().Changed("body") == cmd.Flags().Changed("body-file") {
		return "", fmt.Errorf("Supply exactly one of --body or --body-file.")
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
	fmt.Fprintf(a.out, "Message %d saved in thread %d.\nRead replies: dagger hotline read %d\nRequest ID: %s\n", sent.MessageID, sent.ThreadID, sent.ThreadID, clean(sent.RequestID))
	return nil
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
		if a.org != "" || (orgID == "" && credentials.Token.Type() != "Basic" && credentials.Token.Type() != "OIDC") {
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
