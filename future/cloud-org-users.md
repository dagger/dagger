# Dagger Cloud org user commands

## Context

The `1.0-beta` CLI already has a visible `dagger cloud org` command with
`list`, `info`, and `use`. It also has a hidden top-level `dagger org` alias
backed by the same command tree.

The merged Dagger Cloud API work in
[`dagger.io/future/org-users-api.md`](https://github.com/dagger/dagger.io/blob/main/future/org-users-api.md)
adds token-authenticated GraphQL access for org members and invites:

- `Org.members` and `Org.invites` are available to org members.
- `createOrgInvites` and `deleteOrgInvite` are available to org admins.
- `addOrgUserRole`, `removeOrgUserRole`, and `removeOrgUser` are available to
  org admins.
- API tokens are org-bound and must not add arbitrary members directly with
  `addOrgUserRole(role: MEMBER)`. Membership is added through invites.

That is enough for the CLI to manage org membership without adding a separate
REST API or new auth flow.

## Goals

- Add CLI commands for listing members, listing invites, inviting users,
  revoking invites, promoting admins, demoting admins, and removing members.
- Keep `dagger cloud org` as the documented command surface.
- Keep `dagger org` as the hidden mirror while the beta CLI keeps hidden
  top-level Cloud aliases.
- Reuse the existing Cloud auth, org resolution, `--org`, and `--json`
  behavior.
- Keep membership changes invite-first. The CLI should not expose direct member
  creation by user ID.

## Non-Goals

- SCIM or identity-provider sync.
- Creating Dagger Cloud users.
- Managing API tokens.
- Managing billing seats beyond surfacing Cloud errors.
- Adding pagination before the GraphQL API supports paginated members and
  invites.

## Command Shape

Canonical commands live under `dagger cloud org`:

```text
dagger cloud org member list
dagger cloud org member promote <user-id-or-email>
dagger cloud org member demote <user-id-or-email>
dagger cloud org member remove <user-id-or-email>

dagger cloud org invite list
dagger cloud org invite create <email>...
dagger cloud org invite revoke <invite-id>
```

All commands resolve the target org the same way `org info` does today:

1. `--org <name>`
2. the current org from `dagger login <org>` or `dagger cloud org use <org>`
3. the single org on the current account
4. otherwise fail with `no org specified; use --org or run 'dagger login <org>'`

Do not add positional `[org]` arguments to the new commands. The existing
global `--org` flag is already the Cloud-scoped org selector, and keeping the
target member or invite as the only positional argument makes automation less
ambiguous.

## Member Commands

### List members

```console
$ dagger cloud org member list --org acme
EMAIL              NAME          ROLE    USER ID       CREATED
alice@example.com  Alice Zhang   ADMIN   auth0|alice   2026-05-12T18:30:00Z
bob@example.com    Bob Smith     MEMBER  auth0|bob     2026-05-14T09:10:00Z
```

Role is the effective role:

- `ADMIN` if `roles` contains `ADMIN`
- otherwise `MEMBER`

The command should print an empty-state line when the org has no members,
although that should be rare:

```text
No Dagger Cloud org members found.
```

### Promote a member

```console
$ dagger cloud org member promote bob@example.com --org acme
Promoted bob@example.com to admin in acme.
```

The CLI resolves `<user-id-or-email>` by fetching members and matching:

- exact `userID`
- case-insensitive `email`

It then calls `addOrgUserRole(org: <org-id>, user: <user-id>, role: ADMIN)`.

### Demote an admin

```console
$ dagger cloud org member demote auth0|bob --org acme
Demoted auth0|bob to member in acme.
```

The CLI resolves the target the same way as `promote`, then calls
`removeOrgUserRole(org: <org-id>, user: <user-id>, role: ADMIN)`. The Cloud API
keeps the `MEMBER` role and enforces last-admin protection.

### Remove a member

```console
$ dagger cloud org member remove bob@example.com --org acme
Removed bob@example.com from acme.
```

The CLI resolves the target the same way as `promote`, then calls
`removeOrgUser(org: <org-id>, user: <user-id>)`. The Cloud API owns final
authorization, accepted-invite cleanup, and last-admin protection.

## Invite Commands

### List invites

```console
$ dagger cloud org invite list --org acme
EMAIL              STATUS    INVITE ID       CREATED
new@example.com    PENDING   inv_123         2026-05-20T12:00:00Z
done@example.com   ACCEPTED  inv_456         2026-05-21T12:00:00Z
```

`STATUS` is derived from `acceptedAt`:

- `PENDING` when `acceptedAt` is empty
- `ACCEPTED` otherwise

### Create invites

```console
$ dagger cloud org invite create new@example.com other@example.com --org acme
Created 2 invite(s) in acme.
```

The CLI passes the provided email list to
`createOrgInvites(org: <org-id>, emails: <emails>)`. The Cloud API normalizes
emails, enforces seat checks, sends invite email, and rejects invalid input.

Because the current mutation returns `Boolean!`, the first CLI version should
print a compact success line. In `--json` mode, refetch invites after the
mutation and return the resolved org, submitted emails, and current invite list.

### Revoke an invite

```console
$ dagger cloud org invite revoke inv_123 --org acme
Revoked invite inv_123 in acme.
```

The CLI calls `deleteOrgInvite(org: <org-id>, invite: <invite-id>)`. The Cloud
API enforces org-scoped invite deletion.

`delete` and `rm` should be aliases for `revoke`.

## JSON Output

The existing `--json` flag on `dagger cloud org` should apply to all new
commands.

For reads, return the Cloud objects with stable CLI field names:

```json
[
  {
    "userID": "auth0|alice",
    "email": "alice@example.com",
    "name": "Alice Zhang",
    "nickname": "alice",
    "picture": "https://example.com/alice.png",
    "roles": ["MEMBER", "ADMIN"],
    "createdAt": "2026-05-12T18:30:00Z"
  }
]
```

For member writes, return the resolved org and target:

```json
{
  "org": {
    "id": "org_123",
    "name": "acme"
  },
  "target": "auth0|bob",
  "role": "ADMIN"
}
```

For invite creation, return the submitted emails and the current invite list:

```json
{
  "org": {
    "id": "org_123",
    "name": "acme"
  },
  "emails": ["new@example.com"],
  "invites": [
    {
      "id": "inv_123",
      "email": "new@example.com",
      "orgId": "org_123",
      "createdAt": "2026-05-20T12:00:00Z",
      "acceptedAt": null
    }
  ]
}
```

JSON mode should keep the existing behavior from `cloudClient`: do not start an
interactive login flow. If the user is not authenticated, return an error.

## Cloud Client Additions

Add `internal/cloud/org_users.go` with narrow GraphQL wrappers:

```go
type OrgMember struct {
	UserID    string   `json:"userID"`
	Name      string   `json:"name"`
	Nickname  string   `json:"nickname"`
	Email     string   `json:"email"`
	Picture   string   `json:"picture"`
	Roles     []string `json:"roles"`
	CreatedAt string   `json:"createdAt"`
}

type OrgInvite struct {
	ID         string  `json:"id"`
	Email      string  `json:"email"`
	OrgID      string  `json:"orgId"`
	CreatedAt  string  `json:"createdAt"`
	AcceptedAt *string `json:"acceptedAt,omitempty"`
}

type OrgUsers struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Members []OrgMember `json:"members"`
	Invites []OrgInvite `json:"invites"`
}
```

Suggested methods:

```go
func (c *Client) OrgUsers(ctx context.Context, orgName string) (*OrgUsers, error)
func (c *Client) CreateOrgInvites(ctx context.Context, orgID string, emails []string) error
func (c *Client) DeleteOrgInvite(ctx context.Context, orgID, inviteID string) error
func (c *Client) AddOrgUserRole(ctx context.Context, orgID, userID, role string) error
func (c *Client) RemoveOrgUserRole(ctx context.Context, orgID, userID, role string) error
func (c *Client) RemoveOrgUser(ctx context.Context, orgID, userID string) error
```

Use the existing `doGraphQL` helper, as repo settings, sources, checks, and
billing already do.

The list query should fetch members and invites together so write commands can
resolve users by email without adding another query:

```graphql
query GetOrgUsers($org: String!) {
  org(name: $org) {
    id
    name
    members {
      userID
      name
      nickname
      email
      picture
      roles
      createdAt
    }
    invites {
      id
      email
      orgId
      createdAt
      acceptedAt
    }
  }
}
```

## CLI Implementation

Add `cmd/dagger/org_cloud_users.go`.

That file should:

- add `member` and `invite` subcommands in `newOrgCmd`
- call `cloudCLI.resolveCloudOrg` before every org-scoped operation
- print tabular output with `text/tabwriter`, matching `org list` and
  `billing plans`
- use `writeCloudJSON` for JSON output
- resolve member targets by exact user ID or case-insensitive email
- return a clear error when a member target is not found

Aliases:

- `dagger cloud org invite revoke` aliases: `delete`, `rm`
- `dagger cloud org member remove` alias: `rm`

Avoid adding a generic `user` command or `member add`. Invites are the only
membership creation path.

## Error Behavior

The CLI should pass Cloud errors through with command context:

- invalid email
- invite already exists
- not enough seats
- member not found
- invite not found
- cannot remove or demote the last admin
- forbidden for non-admin users or insufficient API token permissions

Client-side errors should be used only when the CLI can prove the issue before
the mutation, such as an unresolved `<user-id-or-email>` target.

## Tests

Add focused CLI tests when implementing:

- command tree includes visible `dagger cloud org member` and
  `dagger cloud org invite`
- hidden `dagger org` mirror receives the same subcommands while the alias
  remains hidden
- member target resolution matches exact user ID and case-insensitive email
- member target resolution reports not found
- effective role formatting prefers `ADMIN` over `MEMBER`
- invite status formatting maps `acceptedAt` to `ACCEPTED`
- JSON mode does not trigger interactive login

Add `internal/cloud` tests around GraphQL request shape and response decoding if
there is an existing lightweight HTTP test pattern available. Otherwise keep
unit tests at the CLI resolver and formatting layers until a broader Cloud
client test harness exists.

## Status

Proposed. Ready to implement after the Cloud API described by
`dagger.io/future/org-users-api.md` is available to the CLI's configured Cloud
endpoint.
