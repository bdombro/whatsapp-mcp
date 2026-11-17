# WhatsApp MCP: Contributing

[User setup](../README.md) | [Repository setup](../../README.md)

<!-- cpp-templates:begin -->
## Contents

- [Develop and test](#develop-and-test)
- [Unreleased libraries](#unreleased-libraries)
- [Rules](#rules)
- [PR, CI, and release flow](#pr-ci-and-release-flow)
- [Maintainer setup](#maintainer-setup)
- [Dependency bumps](#dependency-bumps)

## Develop and test

```bash
just doctor
just test
just lint
```

Iterate on a single doctest case with `just test "<case>"` (Debug build with ASan + UBSan, `BUGSINK_DSN=""`). For `homebrew-mcp` repos, run `just test-mcp` as well.

## Unreleased libraries

vcpkg reads extra port dirs from `VCPKG_OVERLAY_PORTS`, and an overlay wins over the registry. Point it at a dir of symlinks, one per unreleased lib; manifests never change:

```sh
mkdir -p ~/.vcpkg/overlays/dev
ln -s ~/dev/slimr/<lib>/ports/slimr-<lib> ~/.vcpkg/overlays/dev/slimr-<lib>
export VCPKG_OVERLAY_PORTS=~/.vcpkg/overlays/dev
just test
```

Remove the symlink (or `unset VCPKG_OVERLAY_PORTS`) to build against the released lib again. Keep the export out of shell startup files.

## Rules

- Never push directly to `main`; all changes land through squash-merged pull requests.
- PRs touch no release files (`VERSION`, `CHANGELOG.md`, `versions/`, `Formula/*.rb`, `Casks/*.rb`).
- Write the PR title (plus optional body) as the changelog entry; squash-merge turns it into one commit on `main`.
- Sanitize everything (no secrets, tokens, DSNs, or personal data in commits, logs, or docs).
- Run `just test` (ASan + UBSan) and `just lint` before opening or updating a PR.

## PR, CI, and release flow

- Every PR runs `test.yml`: a `gate` job skips macOS/Linux builds on docs-only PRs, and a single `ci` summary job gates merge.
- Merging to `main` triggers `release.yml` (one release at a time). It collects squash commits since the last tag (skipping docs-only ranges), picks the bump from merged PR labels (`patch` default, or `release:minor` / `release:major`; agents must ask before adding labels), writes `VERSION`/`vcpkg.json`, `CHANGELOG.md` (and `vcpkg x-add-version` for libraries), commits as the GitHub App, tags `vX.Y.Z`, and publishes the GitHub Release (plus assets and `Formula/*.rb` / `Casks/*.rb` updates).
- Pre-releases are a known option (tag/channel), not currently built.

## Maintainer setup

Direct pushes to `main` are blocked; `release.yml` uses a dedicated GitHub App so it can commit release metadata and formula updates without giving humans or agents an admin bypass.

### 1. Create and install the release GitHub App

Create one GitHub App (Settings -> Developer settings -> GitHub Apps):

- **Homepage URL:** any URL (for example, the owner's GitHub profile)
- **Callback / Setup URLs:** blank; **Webhook Active:** unchecked
- **Repository permissions:** `Contents: Read and write`, `Metadata: Read-only`
- **Where can this GitHub App be installed?:** Any account

Record `<app-id>`, generate a private key, and save it locally if needed for manual recovery (it can always be regenerated):

```sh
chmod 600 ~/.ssh/<app-name>.private-key.pem
```

Install the App on each owner (org or user) and select the target repositories.

### 2. Store Actions secrets

Org-level (requires org owner and `admin:org` scope):

```sh
gh auth refresh -h github.com -s admin:org
gh secret set RELEASE_APP_ID --org <owner> --visibility all --body "<app-id>"
gh secret set RELEASE_APP_PRIVATE_KEY --org <owner> --visibility all < ~/.ssh/<app-name>.private-key.pem
gh secret list --org <owner>
```

Per-repo (for user-owned repos or selected repos):

```sh
gh secret set RELEASE_APP_ID -R <owner>/<repo> --body "<app-id>"
gh secret set RELEASE_APP_PRIVATE_KEY -R <owner>/<repo> < ~/.ssh/<app-name>.private-key.pem
gh secret list -R <owner>/<repo>
```

`gh secret list` prints secret names only.

### 3. Protect `main`

Create (or `PUT /repos/<owner>/<repo>/rulesets/<ruleset-id>` to update) the `main` ruleset with App-only bypass, required `ci` check, squash-only merges, and no force push or deletion, then enforce squash-only merges on the repo:

```sh
gh api -X POST /repos/<owner>/<repo>/rulesets --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] }
  },
  "bypass_actors": [
    { "actor_id": <app-id>, "actor_type": "Integration", "bypass_mode": "always" }
  ],
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": false,
        "allowed_merge_methods": ["squash"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": false,
        "required_status_checks": [{ "context": "ci" }]
      }
    }
  ]
}
JSON

gh api -X PATCH /repos/<owner>/<repo> \
  -F allow_squash_merge=true \
  -F allow_merge_commit=false \
  -F allow_rebase_merge=false

gh api /repos/<owner>/<repo>/rulesets
```

### 4. Act as the App (emergency fix or manual release step)

Mint a short-lived JWT (`RS256`, `iat` = now - 60s, `exp` = now + 540s, `iss` = `<app-id>`), exchange it for a 1-hour installation token, and keep all credentials in shell variables only (never echo, log, or commit them):

```sh
app_id=<app-id>
pem=~/.ssh/<app-name>.private-key.pem
b64url() { openssl base64 -e -A | tr '+/' '-_' | tr -d '='; }
now=$(date +%s)
header=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
payload=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now - 60)) $((now + 540)) "$app_id" | b64url)
sig=$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -sign "$pem" | b64url)
jwt="${header}.${payload}.${sig}"

inst_id=$(curl -fsSL -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/<owner>/<repo>/installation" | jq -r .id)
token=$(curl -fsSL -X POST -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
  "https://api.github.com/app/installations/${inst_id}/access_tokens" | jq -r .token)
```

Use `$token` with `gh` or `git push`, committing as the App's bot identity (`gh api /users/<app-slug>%5Bbot%5D --jq .id` gives `<bot-user-id>`):

```sh
git -c user.name="<app-slug>[bot]" \
    -c user.email="<bot-user-id>+<app-slug>[bot]@users.noreply.github.com" \
    commit -m "fix: ..."
git push "https://x-access-token:${token}@github.com/<owner>/<repo>.git" HEAD:main
GH_TOKEN="$token" gh release create ...
```

Tokens expire after 1 hour; revoke early when done:

```sh
curl -fsSL -X DELETE -H "Authorization: Bearer $token" -H "Accept: application/vnd.github+json" \
  https://api.github.com/installation/token
unset jwt token
```

## Dependency bumps

Bump a library baseline in `vcpkg-configuration.json` only when a bug fix, new API, or security fix requires it; otherwise wait for the next `just upgrade-deps`.
<!-- cpp-templates:end -->

## Repository tips

The stateless MCP server and JSON CLI are implemented in C++26 on the `slimr-*` libraries, aligned to the cpp-templates `homebrew-mcp` template. It reads `messages.db` and uses the companion Go `whatsapp-client` only for sends and media downloads.

## Cryptographic Session-Key Isolation & SQLite Views

`whatsapp.db` (managed by `whatsmeow`) holds both contact names/LID mappings and sensitive Signal protocol session keys. Before running **any** query (including arbitrary user/agent queries in `sql`), `src/db.hpp` prepares an isolated read-only connection:

1. Opens `messages.db` with `SQLITE_OPEN_READONLY` and registers the deterministic `wa_utc(timestamp)` C++ scalar function (normalizing RFC3339, Go `time.String()`, and ISO timestamps to UTC `YYYY-MM-DD HH:MM:SS`).
2. `ATTACH`es `whatsapp.db` in `mode=ro`.
3. Copies only non-sensitive contact metadata (`t_names`, `t_contacts`, `t_lid_map`) into in-memory `TEMP` tables.
4. Immediately **`DETACH`es `whatsapp.db`** so Signal private keys, identity keys, and session ratchets are completely unreachable from the SQLite connection.
5. Creates `TEMP` views `v_messages`, `v_chats`, and `v_contacts` (resolving `@lid` and `@s.whatsapp.net` JIDs to human-readable contact and chat names).
6. Enables `PRAGMA query_only = ON`.

## HTTP Transport, OAuth 2.1 & Browser Connect UX

When launched via `whatsapp-mcp http [--host <addr>] [--port <n>] [--log-file <path>]` (default bind `127.0.0.1:10008`; OAuth metadata and `publicBaseUrl` advertise `http://127.0.0.1:<port>` unless `--host` names a specific address), the server exposes `POST /mcp` protected by OAuth 2.1 + PKCE (`S256`):
- `GET /.well-known/oauth-protected-resource` (RFC 9728) & `GET /.well-known/oauth-authorization-server` (RFC 8414)
- `POST /register` (RFC 7591 dynamic client registration restricted to loopback redirect URIs)
- `GET /authorize` + `GET /authorize/qr` + `POST /authorize/approve` + `POST /authorize/cancel` (CSRF-protected browser Connect page; renders live QR SVG when `whatsapp-client` is unpaired, or shows the already-paired JID for explicit owner approval without touching `whatsapp.db`)
- `POST /token` (authorization code + PKCE `S256` + refresh token rotation with replay family revocation) and `POST /revoke` (revokes MCP access without disconnecting the WhatsApp session)

### CLI Examples

```bash
# Search messages across all chats using FTS5 BM25
./build/bin/whatsapp-mcp list_messages '{"query":"boiler","limit":10}'

# Bulk export messages since a local date
./build/bin/whatsapp-mcp export '{"since":"2026-11-01"}'

# Run a read-only SQL query against the isolated views
./build/bin/whatsapp-mcp sql '{"sql":"SELECT chat_name, COUNT(*) AS n FROM v_messages GROUP BY chat_jid ORDER BY n DESC LIMIT 10"}'

# Inspect the SQL view schema
./build/bin/whatsapp-mcp schema
```

## Building & Testing

Prerequisites, Linux notes and the release flow are in the
[repository contributor reference](../../docs/contributing.md#building--testing);
`just doctor` checks them. Dependencies resolve from the pinned `slimr-*`
registries in `vcpkg-configuration.json` and install under `build/deps/`
automatically before configure/build/test/lint; CMake manifest auto-install is
disabled.

Debug sanitizer and Release IPO graphs live in `build/cmake/Debug/` and `build/cmake/Release/`; only Release binaries are emitted to `build/bin/`. Both enable libc++ FAST hardening and zero automatic-variable initialization; Debug adds ASan and fatal UBSan. CTest runs database and mock-socket fixtures with a finite timeout, `BUGSINK_DSN=""`, and isolated repo-local HOME/XDG/temporary directories and `HOMEBREW_PREFIX`.

```bash
just test              # C++26 unit & isolation tests (wa_utc, key isolation, FTS5, views, export, mock socket) with ASan + UBSan
just test "<filter>"   # Build the test target and run matching doctest cases
just build             # Optimized IPO Release binary at build/bin/whatsapp-mcp (no tests)
just fmt               # Format C++ sources and justfile
just lint              # Format check + clang-tidy (.clang-tidy)
just test-mcp          # Bun ephemeral HTTP MCP 2026-07-28 discovery (OAuth-protected), isolated
just clean             # Remove build/ only; leaves user data untouched
```

For local development without touching the installed service (`10008`), run `./build/bin/whatsapp-mcp http --port 20008` with a scratch `WHATSAPP_CLIENT_DATA_DIR`.

`scripts/test-mcp.ts` runs the server against a scratch `WHATSAPP_CLIENT_DATA_DIR` with Bugsink disabled; it never opens production databases, invokes tools, or starts/connects to the daemon.
