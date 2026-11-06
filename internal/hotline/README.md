# Hotline

`dagger hotline` exchanges messages with the Dagger team. It shows help when
called without a command. Use `dagger login` first. An active subscription
and a running engine are not required.

Messages and attachments are shared with your organization and the Dagger
team. `--org NAME` selects an organization. Threads have one type.

```sh
dagger hotline new --body 'The engine will not start.' --attach engine.log
dagger hotline inbox
dagger hotline read 1 --unread
dagger hotline reply 1 --body 'Here is an update.'
```

Use the returned IDs. List, inbox, and read return 20 results by default.
Set `--limit 1–100`. Continue with `--after NEXT_CURSOR`; JSON includes
`next_cursor` only when another page exists. Lists can change as replies arrive.

`read --unread` shows only unread messages from other accounts. `read --peek`
leaves them unread. Read positions belong to an account and thread. Agents
that use the same account share a read position. A failed output write does
not mark messages as read. Skipping earlier unread messages with `--after`
requires `--peek`; otherwise the read acknowledgment fails.

`watch` reports current unread threads, then checks every 5 seconds. It leaves
messages unread. Stop with Ctrl-C. With `--json`, each line is a thread summary.
A notice can cover several new messages. After a service error, it retries
with a delay of up to one minute.

## Message input

Use exactly one of `--body TEXT`, `--body-file PATH`, or `--editor`.
`--body-file -` reads standard input. `--editor` uses VISUAL, then EDITOR,
then vi. Commands never prompt or open an editor without this flag.
The text limit is 64 KiB. If an editor send fails, the error gives the saved
draft path. Retry with `--body-file PATH` and the returned `--request-id`.

Use `--attach PATH` for each selected file. Paths are relative to the current
directory. Only regular files are accepted. No files are collected by default.
Directories, patterns, and links that escape the directory are rejected.

Each message can attach up to 100 files and 1 MiB total. The CLI reads the
bytes directly. Cloud stores them with the message.
Later file changes do not change the attachment. Download writes a ZIP file
without extraction or replacement of an existing file.

## Agent use

Ask early when you need help. A complete defect report is not required.
Put `--json` before other flags so flag errors also use JSON. Results go to
standard output; errors go to standard error. Exit codes are 0 for success,
1 for an API error, 2 for an input or local error, and 3 for an account or
permission error. A read acknowledgment can fail after output was written;
check the exit code.

Choose a `--request-id` before sending. Retry with the same ID and input after
a timeout. Cloud returns the first result without another message. Changed
input with the same ID returns `request_conflict`. Without this flag, the CLI
creates an ID and returns it on success or request failure.

## Local mock

`--mock FILE` or `DAGGER_HOTLINE_MOCK` uses a local JSON file. No engine,
network, service, or Cloud account is needed.

`--mock-user` selects `member` (default), `admin`, `team`, or `other`.
The first three share an organization but have separate read positions.
`other` belongs to another organization. Use `team` to simulate a team reply.

```sh
export DAGGER_HOTLINE_MOCK=/tmp/hotline-demo.json
dagger hotline new --body 'How do I start?'
dagger hotline reply 1 --mock-user team --body 'Tell us what happened.'
dagger hotline inbox
```

Old prototype files are rejected without changes. Use a new file path.
