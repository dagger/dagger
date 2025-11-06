// Send notifications
//
// Supports Discord & Slack.
package main

import (
	"context"
	"errors"
	"fmt"

	"main/internal/dagger"

	"github.com/disgoorg/disgo/webhook"
	"github.com/slack-go/slack"
	"go.opentelemetry.io/otel/trace"
)

type Notify struct{}

// Message a Discord webhook: `dagger call discord --webhook-url=env:DISCORD_WEBHOOK --message="👋 from Dagger notify module"`
func (n *Notify) Discord(
	ctx context.Context,
	webhookURL *dagger.Secret,
	message string,
) (string, error) {
	if message == "" {
		return "", errors.New("--message cannot be an empty string")
	}

	url, err := webhookURL.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	client, err := webhook.NewWithURL(url)
	if err != nil {
		return "", err
	}
	defer client.Close(ctx)

	m, err := client.CreateContent(message)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("MESSAGE SENT AT: %s\n%s\n", m.CreatedAt, m.Content), err
}

// Message a specific Slack channel: `dagger call slack --token=env:SLACK_TOKEN --channel-id=C07PBDE3U57 --color="#FC0" --message="👋 from Dagger notify module"`
func (n *Notify) Slack(
	ctx context.Context,
	// The slack token to authenticate with the slack organization
	token *dagger.Secret,
	// The sidebar color of the message
	color string,
	// The content of the notification to send
	message string,
	// The channel where to post the message
	channelId string,
	// Set a title to the message
	// +optional
	title string,
	// Set a footer to the message
	// +optional
	footer string,
	// Set an icon in the footer, the icon should be a link
	// +optional
	footerIcon string,
	// Add an image in the message
	// +optional
	ImageUrl string,
	// The thread id if we want to reply to a message or in a thread
	// +optional
	threadId string,
) (string, error) {
	clearToken, err := token.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	api := slack.New(clearToken)
	attachment := slack.Attachment{
		Color:      color,
		Text:       message,
		MarkdownIn: []string{"text"},
		Title:      title,
		Footer:     footer,
		FooterIcon: footerIcon,
		ImageURL:   ImageUrl,
	}

	options := []slack.MsgOption{
		slack.MsgOptionText("", false),
		slack.MsgOptionAttachments(attachment),
		slack.MsgOptionAsUser(true),
	}

	if threadId != "" {
		options = append(options, slack.MsgOptionTS(threadId))
	}

	_, ts, err := api.PostMessage(
		channelId,
		options...,
	)

	if err != nil {
		return "", err
	}

	return ts, nil
}

// helper to return a dagger cloud trace link from the OTEL data in ctx.
// useful as input to "message" to link your slack or discord notification back up to dagger cloud.
func (n *Notify) DaggerCloudTraceUrl(
	ctx context.Context,
) (string, error) {
	spanContext := trace.SpanFromContext(ctx).SpanContext()
	if !spanContext.IsValid() {
		return "", errors.New("unable to find trace id in context: check your otel configuration")
	}

	return fmt.Sprintf("https://dagger.cloud/dagger/traces/%s", spanContext.TraceID().String()), nil
}
