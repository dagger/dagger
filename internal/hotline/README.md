# Hotline

`dagger hotline` sends persistent messages to the Dagger team. A thread holds
the first message and all replies. Use `dagger login` first. An active paid
subscription is not required. Hotline needs no running engine.

| Command | Purpose |
| --- | --- |
| `setup --outcome success\|blocked\|stopped` | Report the first setup attempt |
| `feedback` | Report the Dagger experience |
| `question` | Ask for help |
| `bug` | Report a defect |
| `billing` | Ask about billing; requires organization admin access |
| `suggestion` | Suggest an improvement |
| `list` | List the latest 50 threads |
| `read THREAD` | Read up to 100 messages |
| `reply THREAD` | Add a message |
| `attachments MESSAGE --output FILE.zip` | Download attached files |

Create and reply require `--body TEXT` or `--body-file PATH`. Use `--body-file -`
for standard input. The text limit is 64 KiB. `--org NAME` selects a Cloud
organization. Other commands use the current organization.

```sh
dagger hotline setup --outcome blocked --body-file - \
  --attach dagger.toml --request-id setup-attempt-1 --json
dagger hotline read 1 --json
dagger hotline reply 1 --body 'Here is an update.' --json
```

Use the returned IDs. Replies can arrive later. Read again with
`read THREAD --after MESSAGE_ID`. Use the last returned message ID.
If a page has 100 messages, request the next page.

## Attachments

Use `--attach PATH` for each named file. Paths are relative to the current
directory. Only regular files are accepted. No files are collected by default.
Directories, patterns, and links that escape the directory are rejected.

Each message can attach up to 100 files and 1 MiB total. The CLI reads the
bytes directly. Cloud stores a fixed copy with the message. Later file changes
do not change the attachment. Download writes a ZIP file without extraction
or replacement of an existing file.

## Agent use

Commands do not prompt or open a browser. Put `--json` before other flags so
flag errors also use JSON. Results go to standard output. Errors go to standard
error. Exit codes are 0 for success, 1 for an API error, 2 for an input error,
and 3 for an account or permission error.

Choose a `--request-id` before sending. Retry with the same ID and input after
a timeout. Cloud returns the first result without another message. A changed
payload with the same ID returns `request_conflict`. Without this flag, the CLI
creates an ID and returns it on success or request failure.

Ask early when blocked. A complete defect report is not required. Use `setup`
to report the first setup result, then reply on that thread with updates.

## Local mock

`--mock FILE` or `DAGGER_HOTLINE_MOCK` uses a local JSON file. No engine,
network, service, or Cloud account is needed. The same CLI commands work.
A file lock and atomic replacement protect concurrent writes.

`--mock-user` selects `member` (default), `admin`, `team`, or `other`.
The first three share an organization. `other` belongs to another organization.
Use `team` to simulate a team reply. Billing requires `admin` or `team`.

```sh
export DAGGER_HOTLINE_MOCK=/tmp/hotline-demo.json
dagger hotline question --body 'How do I start?'
dagger hotline reply 1 --mock-user team --body 'Tell us what happened.'
```
