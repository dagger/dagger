# Dagger Notify Module

## Discord

### CLI example

Send Discord notifications via a [Webhook URL](https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks):

```console
export DISCORD_WEBHOOK=<your-discord-webhook-here>

dagger call -m github.com/gerhard/daggerverse/notify \
    discord --webhook-url env:DISCORD_WEBHOOK --message 'Hi from Dagger Notify Module 👋'
```

## Slack

```shell
dagger call slack --help
Send a message to a specific slack channel or reply in a thread

Usage:
  dagger call slack [flags]

Flags:
      --channel-id string    The channel where to post the message
      --color string         The sidebar color of the message
      --footer string        Set a footer to the message
      --footer-icon string   Set an icon in the footer, the icon should be a link
      --image-url string     Add an image in the message
      --message string       The content of the notification to send
      --thread-id string     The thread id if we want to reply to a message or in a thread
      --title string         Set a title to the message
```

Message looks like that from a channel:
![slack 0](../.github/assets/slack_0.png)

Then the message in the thread looks like:
![slack 1](../.github/assets/slack_1.png)

### CLI

```shell
export SLACK_TOKEN="xoxb-not-a-real-token-this-will-not-work"

dagger call slack \ 
  --channel-id="<CHANNEL_ID>" \
  --token=env:SLACK_TOKEN \
  --color="#2596be" \
  --message="Hello world!" \
  --title="My title" \
  --footer="My footer" \
  --footer-icon="https://avatars.githubusercontent.com/u/78824383?s=280&v=4" \
  --image-url="https://framerusercontent.com/images/bJT2c1WWr6bzO8aoEkh0Zz1Ra8.webp"

dagger call slack \
  --channel-id="<CHANNEL_ID>" \
  --token=env:SLACK_TOKEN \
  --color="danger" \
  --message="This is a reply in a thread" \
  --thread-id="ID from the previous output command"
```

### Golang

```go
package main

import (
	"context"
)

type Demo struct{}

func (m *Demo) SlackExample(ctx context.Context, token *Secret) error {
	threadId, err := dag.
		Notify().
		Slack(
			ctx,
			token,
			"#2596be",
			"Hello world!",
			"<CHANNEL_ID>",
			NotifySlackSendMessageOpts{
				Title:      "My title",
				Footer:     "My footer",
				FooterIcon: "https://avatars.githubusercontent.com/u/78824383?s=280&v=4",
				ImageURL:   "https://framerusercontent.com/images/bJT2c1WWr6bzO8aoEkh0Zz1Ra8.webp",
			},
		)
	if err != nil {
		return err
	}

	// Reply to the previous message
	_, err = dag.
		Notify().
		Slack(
			ctx,
			token,
			"danger",
			"This is a reply in a thread",
			"<CHANNEL_ID>",
			NotifySlackSendMessageOpts{
				ThreadID: threadId,
			},
		)

	return err
}
```
