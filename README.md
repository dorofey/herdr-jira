# herdr-jira

Jira Cloud issues and projects in a Herdr popup, adapted from
[mrolafsson/herdr-linear](https://github.com/mrolafsson/herdr-linear) under its MIT license.

Browse assigned issues grouped by status, search by key or title, read descriptions,
change status using the issue's available workflow transitions, and open or create
an issue worktree. **Start** checks ownership, assigns unassigned issues to you,
moves them into progress, and hands a Jira issue URL to the worktree's coding agent.
Includes project browsing, quick issue creation, attachments, multiple Jira sites,
Herdr theme colours, mouse support, sidebar issue labels, and an offline demo.

## Build and link

Requires Go 1.26.8 or newer and Herdr 0.9.0 or newer, on macOS or Linux.

```sh
sh scripts/build.sh
herdr plugin link "$PWD"
herdr plugin action invoke herdr-jira.open
```

Optional binding in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "prefix+alt+j"
type = "plugin_action"
command = "herdr-jira.open"
description = "Jira: issues and projects"
```

Then run `herdr server reload-config`. Rebuild after changing the source.

## Sign in

Open the picker and press Enter. Enter each field when prompted:

1. Your Jira Cloud site, e.g. `https://your-team.atlassian.net`.
2. Your Atlassian account email.
3. An Atlassian API token **without scopes**, created in
   [Atlassian account settings](https://id.atlassian.com/manage-profile/security/api-tokens).

Input is hidden in the popup and interactive CLI. Alternatively:

```sh
bin/herdr-jira login
bin/herdr-jira status
bin/herdr-jira logout
```

Automation can provide `JIRA_URL`, `JIRA_EMAIL`, and `JIRA_API_TOKEN` through the
environment to `login`. Never put real credentials in repository files or command
arguments. Sign-in validates your account before storing credentials.

macOS uses the login Keychain. Linux uses Secret Service; `token_store: "auto"`
falls back to a private file if the keyring is unavailable. Set `"token_store":
"keyring"` to require the keyring, or `"file"` to explicitly use a file with mode
0600. Files are not encrypted. Credentials are stored under this plugin's own
service and never share the Linear plugin's tokens.

Sign out forgets credentials and cached issue lists on this computer. To revoke
the API token, use Atlassian account settings. Expired or revoked tokens require
signing in again. Each site has separate credentials; Ctrl+T switches sites and
remembers which site belongs to the current repository.

## Controls

| Location | Key | Action |
|---|---|---|
| List | Left / Right | Assigned issues, projects, recently created issues |
| List | Type | Filter; full keys such as `APP-123` can find other issues |
| List | Enter | Details |
| List | Ctrl+C | New issue |
| List | Ctrl+S | Start issue |
| List | Ctrl+O | Open in Jira |
| List | Ctrl+R | Refresh |
| List | Ctrl+T | Switch site |
| Issue | W | Open or create worktree |
| Issue | S | Start issue and hand it to the agent |
| Issue | C | Choose an available workflow transition |
| Issue | O | Open in Jira |
| Issue | Y | Copy branch name |
| Form | Tab / Shift+Tab | Move between fields |
| Form | Ctrl+S | Create issue |
| Form | Ctrl+F | Pick attachments via `herdr-file-transfer` |
| Any | Escape | Back / close |

## Configuration

Place `config.json` in Herdr's configuration directory for `herdr-jira`.
The plugin honours `HERDR_PLUGIN_CONFIG_DIR` and `HERDR_PLUGIN_STATE_DIR`.
See [config.example.json](config.example.json).

`repos` maps Jira project keys to repository paths. For multiple sites,
`your-team.atlassian.net/APP` overrides `APP`. New issue branches are generated as
`app-123`, keeping the branch stable when a title changes. New worktrees use the remote's default branch, falling back
to `origin/main`, unless `base` is configured. The default agent prompt contains
the Jira issue key and URL; `start_prompt` supports `{identifier}`, `{title}`, `{url}`.

## Jira specifics

- Uses REST v3 and enhanced `/search/jql` pagination.
- Status changes use **transition IDs**, fetched for each issue when the status
  picker opens. Jira permissions and required workflow fields still apply.
- Start refuses issues assigned to a colleague and completed issues. If multiple
  transitions lead into progress, choose one manually before starting.
- Assignment and transition are separate Jira operations. If a later step fails,
  the error describes the partial result; created worktrees are preserved.
- Quick creation uses the project's Task issue type when available, otherwise
  its first standard issue type. Jira chooses the initial status. Projects with
  additional required custom fields must use Jira's creation form.
- Priority labels map to the site's named priorities, never hardcoded Jira IDs.
  Choose None to use the project's default priority.
- Creation descriptions are plain text; reading supports common Atlassian
  Document Format blocks as Markdown. Embedded media is marked for viewing in Jira.
- Attachments are uploaded after creation, up to 25 MiB each. An upload failure
  keeps the created issue and shows a warning, so retrying cannot create duplicates.
- Scoped API tokens, OAuth, and Jira Data Center are not implemented.

API references: [authentication](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/),
[search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/),
[issues and transitions](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/),
[attachments](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-attachments/).

## Demo and validation

```sh
bin/herdr-jira picker --demo
go test ./...
```

Demo data is fictional and performs no Jira, git, or Herdr mutations. Tests use an
in-memory HTTP transport for authentication, pagination, transitions, ownership,
attachment payloads, URL validation, and description rendering. Live Jira calls
and actual Herdr worktree/agent handoff require an authenticated user session.

## License

MIT. Original Herdr UI and integration code copyright Magnús Ólafsson;
see [LICENSE](LICENSE). Jira integration changes are provided under the same license.

## Acknowledgements

Thank you to [Magnús Ólafsson (@mrolafsson)](https://github.com/mrolafsson)
for creating [herdr-linear](https://github.com/mrolafsson/herdr-linear) and
sharing it under the MIT license. Its terminal UI and Herdr integration provided
the foundation for this Jira plugin.
