# auth

The auth command group manages authentication with Slack workspaces. Credentials are stored in the macOS Keychain and reused across CLI invocations.

## auth login

Log in to a Slack workspace and save the session credentials.

Usage:

```
slackcli auth login --workspace <name>
```

Flags:

- `--workspace` (required) — Slack workspace name or domain. Normalised to bare domain (e.g. myorg → myorg.slack.com, https://myorg.slack.com/client/... → myorg.slack.com).
- `--firefox` — Use Firefox browser instead of Chromium. Browser opens visibly so you can log in.

Process:

- Opens a browser window at the workspace URL pointing to the login page.
- Uses a persistent browser profile at `/tmp/slackcli-browser-profile` so you only need to log in once; subsequent runs reuse the saved session (cookies, localStorage).
- Monitors network requests and responses to extract the xoxc token from `localConfig_v2` in localStorage.
- Extracts the xoxd cookie from browser storage state after login completes.
- Saves both token and cookie to the macOS Keychain as a single generic-password item keyed by workspace domain.
- Timeout: 5 minutes. After 5 minutes, the operation aborts with an error.
- Ctrl+C cancels the login gracefully. Press Ctrl+C a second time to force-kill the browser after 3 seconds.

## auth reauth

Re-authenticate with a Slack workspace when your session has expired or been revoked.

Usage:

```
slackcli auth reauth --workspace <name>
```

Flags: Same as `auth login` (`--workspace` required, `--firefox` optional).

Process:

- Deletes the existing Keychain entry for the workspace.
- Opens the browser and repeats the login flow (see `auth login`).
- Use when the session has been revoked, token has expired, or you need to switch accounts for a workspace.

## auth status

Check the validity of saved workspace credentials.

Usage:

```
slackcli auth status [--workspace <name>]
```

Flags:

- `--workspace` (optional) — Show status of a single workspace. Omit to list all saved workspaces.

Process:

- For each saved workspace (or the specified workspace), calls the Slack API method `auth.test` to verify the token is still valid.
- Displays the result with user, team, and credential age.

Output format:

- Valid: `  <workspace>  OK  (user: alice, team: My Org, saved 3h ago)`
- Expired or invalid: `  <workspace>  EXPIRED  (error: invalid_auth, saved 5d ago)` followed by a remediation hint (e.g. "run slackcli auth reauth --workspace <name>").
- Network error: `  <workspace>  [network error: ...]`

## auth logout

Remove saved credentials for a workspace from the Keychain.

Usage:

```
slackcli auth logout --workspace <name>
```

Flags:

- `--workspace` (required) — Workspace to log out from.

Process:

- Deletes the Keychain entry for the workspace. No API call is made.
- If no entry exists for the workspace, prints a message and exits with status 0.

## auth default

Get or set the default workspace used when no `--workspace` flag is given.

Usage:

```
slackcli auth default [--workspace <name>]
```

Flags:

- `--workspace` (optional) — Workspace domain to set as default. Omit to print the resolved default.

Behaviour:

- **With `--workspace`:** Canonicalises the name (e.g. myorg → myorg.slack.com) and stores it as the persistent default in the Keychain under account `__default__`.
- **Without `--workspace`:** Prints the currently resolved default workspace.
- **Resolution order when `--workspace` is not provided to other commands:**
  1. Stored default (set with this command)
  2. Single saved workspace (implicit when only one exists)
  3. Error — ambiguous; use `--workspace` or set a default

Examples:

```
slackcli auth default --workspace myorg
slackcli auth default
```


## auth workspaces

List all Enterprise Grid workspaces accessible with the saved credentials. Backfills grid metadata in the Keychain so that cross-workspace channel lookups (`read`, `history`) can scope retries to the correct grid.

Usage:

```
slackcli auth workspaces [--workspace <name>]
```

Flags:

- `--workspace` (optional) — Workspace whose token is used for the lookup. Omit to use the default workspace.

Process:

- Loads the saved credentials for the workspace.
- Calls `client.userBoot` to enumerate every workspace visible to the xoxc token.
- Updates the saved Keychain entry with the discovered workspace domains (`grid_workspaces` field).
- Prints the full list with IDs.

Output example:

```
Grid workspaces for myorg.slack.com (4 total):
  myorg-blue.slack.com   (id: T0AB1234)
  myorg-dev.slack.com    (id: T0CD5678)
  myorg-ops.slack.com    (id: T0EF9012)
  myorg-eng.slack.com    (id: T0GH3456)
```

This command is also run automatically at login time on Enterprise Grid accounts; `auth workspaces` is useful for backfilling metadata after upgrading from an older credential.

## Implementation notes

The auth command is implemented across several files:

- `cmd/auth.go` contains Layer 1 pure functions: `AuthStatus`, `AuthLogout`, `AuthDefault`, `AuthWorkspaces`, `FormatEntryStatus`, `CanonicalDomain`.
- `main.go` defines `makeLoginRunE` which injects Playwright browser logic and signal handling for login and reauth. After a successful save it calls `AuthTest` + `GridWorkspaces` to populate `EnterpriseID` and `GridWorkspaces` on the keychain entry (best-effort).
- `internal/browser/extractor.go` manages Playwright session lifecycle and credential extraction from the browser environment.
- `internal/keychain/keychain.go` provides Keychain read, write, delete, and list operations. `Entry` carries two optional Enterprise Grid fields: `enterprise_id` and `grid_workspaces` (slice of sibling workspace domains).
- `internal/keychain/default.go` provides default workspace management: `SetDefault`, `GetDefault`, `DeleteDefault`, `ResolveDefault`. The default is stored as a Keychain item with service=`slackcli`, account=`__default__`, and value=workspace domain string. `DeleteDefault()` is a no-op when no default is stored.
- `internal/slack/grid.go` implements `(*Client).GridWorkspaces(ctx, workspace)` which calls `client.userBoot` and returns `[]GridWorkspace{ID, Domain}`.

Workspace names and URLs are normalised to bare domains via the `CanonicalDomain` function.
