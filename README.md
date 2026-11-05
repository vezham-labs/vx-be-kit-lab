# Vezham backend kit · Tamizhi

Tamizhi runs as an independent Go companion with `serve`, `chat`, and `status` commands, plus an explicitly configured hosted API mode. Standalone and embedded React applications remain static/prerendered clients. Provider credentials, agent tools, authentication and durable conversation state stay on the backend.

## Local companion POC

```sh
pnpm exec nx run tamizhi-api:build
./dist/tamizhi serve
```

The local service listens on `127.0.0.1:7331`. In standalone or embed, open
**Connect companion** and click **Connect**. The browser supplies its actual
origin; an unrecognized origin appears in the running CLI with a verification
code. Compare the code and origin with the UI, then enter `y` to approve and
remember it, or reject with `n`/Enter. With no terminal input, requests are rejected.
Approved origins are saved in owner-only `~/.vezham/tamizhi/allowed-origins.json`
(or the selected `--data-dir`). `--origin` is optional preapproval and is saved
there too. The allowlist supports up to 32 origins.

Browser sessions stay in memory. After reload or companion restart, click
**Connect** again; an approved origin does not need another terminal prompt.
Then use **AI settings** beside the connection control to configure providers.
Hosted UI origins need HTTPS and may require browser local-network permission.
HTTP `*.vezham.local` development apps use the shared Vite preset's local relay;
restart the frontend dev server after its configuration changes. `http://localhost`
connects directly. Connections use terminal approval for new origins; no manual
pairing-code interface is required.

```sh
./dist/tamizhi status
./dist/tamizhi chat --permission safe 'What is the latest React version?'
./dist/tamizhi chat --permission safe --json 'What is the latest React version?'
```

Local mode uses owner-only state under `~/.vezham/tamizhi/`, SQLite conversations,
and encrypted provider keys. It does not require Neon, OIDC, Node, an extension,
or a hosted Tamizhi service at runtime. Go owns the existing agent loop; a
TanStack AI connection adapter consumes structured HTTP/SSE events in the UI.
The web plugin uses opt-in DeepSeek Search and retrieves public server-rendered pages using bounded search/open/read
tools, source tracking and the existing DNS-pinned network client. It does not
render JavaScript, inherit browser sessions or bypass CAPTCHA. Leo is the built-in default and requires no model-provider key. Optional BYOK
models remain available for broader language-model reasoning.

The existing configured hosted API is `./dist/tamizhi api --config ...`; its
OIDC, provider OAuth and Neon settings below remain available independently.

## Leo: built-in tool workflows

A fresh companion advertises `leo/default` (**Leo**) without provider keys. Leo
runs through the existing provider plugin and agent loop, with the same owner
checks, approvals, cancellation, budgets and durable SSE as BYOK models. Auto
selects Leo by default. Explicit BYOK selections continue to use that provider.

Use plain language such as “Leo movie”, “What is the latest React version?”,
“Remember my launch is Friday”, or “Show my notes about the launch”. Conversational
prefixes such as “please” and “can you” are accepted. Slash commands are optional
shortcuts: `hi`, `/help`, `/tools`, `/search latest React version`,
`/read https://react.dev/versions`, `/remember my launch is Friday`, and
`/recall launch`. `search for ...`, `look up ...`, and `read https://...` are
also supported. `/tool name {"argument":"value"}` invokes an installed skill
or MCP tool using its declared schema. Web commands require the `web` plugin;
local companion mode enables it. Other tools still need their configured
resources or service connections.

Leo does not use a language model. It routes supported requests through fixed
workflows, quotes successfully read web sources with citations, and formats
actual tool results. Unsupported/ambiguous requests ask for clarification.
It never treats retrieved prose as workflow instructions. Plain public-topic lookups such as “Leo movie” and informational questions use
web research. Recognized private/app requests and action requests ask for
clarification instead of being inferred as public searches. Context compaction uses a
bounded verbatim extract while retaining the full durable transcript.

`LeoPlugin(customWorkflows...)` accepts additional trusted Go workflow planners
when composing a runtime. Planners return steps/tool calls; they do not execute
tools directly. Enablement remains managed as `provider.leo` through the existing
plugin controls. This is Tamizhi's workflow engine, not a trained in-house LLM.

## Workspace

Go is the primary backend language. Supporting TypeScript applications and libraries share this Nx workspace, including the existing Vx CLI, framework plugins, linting, tests, formatting and package release tooling. Use `@vx/go` for Go projects and `@vx-cli/react` for TypeScript/React projects. Runtime Go dependencies belong in `go.mod`; JavaScript tooling and application dependencies belong in package manifests.

| Project                | Responsibility                                                                     |
| ---------------------- | ---------------------------------------------------------------------------------- |
| `tamizhi-api`          | HTTP process, graceful shutdown and configuration                                  |
| `tamizhi-core-go`      | Plugin lifecycle, agent loop, providers, auth, tools, persistence and transport    |
| `tamizhi-contracts-go` | Go wire DTOs and generated TypeScript/JSON Schema/OpenAPI                          |
| `@vx/go`               | Local Nx application/library generator compatible with this workspace's Nx version |

The toolchain is pinned in `.go-version`. Setup verifies the official Go archive SHA-256. SDK and module/build caches are local, ignored directories; no global Go installation is required.

```sh
pnpm install --frozen-lockfile
pnpm setup:go
cp config/tamizhi.example.json config/tamizhi.json
# Export DATABASE_URL with the Neon connection string (keep its TLS options).
# Set a random TAMIZHI_DEV_TOKEN of at least 24 characters for local API access.
# Users connect their own providers in Tuning → AI settings after signing in.
pnpm nx run tamizhi-api:build
./dist/tamizhi api --config config/tamizhi.json
```

Configuration contains environment variable names, never provider keys. The development bearer token authenticates a specific user; use OIDC browser sessions for deployed apps. Do not place a development token in a static frontend build.

## Linked Neon project

The local backend is linked to `tamizhi` (`late-flower-50992894`) in organization `org-summer-base-09791622`, AWS Asia Pacific (Singapore), on the `main` branch (Postgres 18).

[Open the Neon project](https://console.neon.tech/app/projects/late-flower-50992894/).

Neon CLI generated `.env.local` with pooled `DATABASE_URL`, direct `DATABASE_URL_UNPOOLED` and `NEON_BRANCH`. That file and the `.neon` context are git-ignored; the credentials file has owner-only permissions. Inject these variables into the Go process when running it; the Go binary does not automatically load dotenv files.

The project-scoped Codex MCP configuration is `.codex/config.toml`. Restart Codex in this backend repository to load it; MCP may request its own OAuth sign-in on first use. Official `neon`, `neon-postgres` and `neon-postgres-branches` skills are installed in `.agents/skills`. The Skill Installer was used after the system npm launcher prevented Neon's skills subcommand from completing.

Both pooled and direct URLs passed live TLS checks through Go `pgx`. Full schema/CRUD, ownership, pagination, attachment, replay and HTTP streaming tests also passed on an isolated Neon child branch. That branch was deleted after verification; the main branch was unchanged.

## SSO and social sign-in

Standalone and embedded Tamizhi share a visible account control. Anonymous `GET /api/tamizhi/auth/session` returns only login availability, configured connection labels and the current session's identity. Sign-in uses OIDC authorization-code flow with PKCE, nonce, one-use state and verified ID tokens; provider access tokens are not exposed to the browser.

`config/tamizhi.sso.example.json` is a complete **configuration template**, not a live identity service. Replace its example domains, register the callback with your OIDC broker, and set `TAMIZHI_OIDC_CLIENT_SECRET` on the server. Configure Google, Microsoft, GitHub and organization SSO as broker identity providers with the matching aliases. The template uses Keycloak `kc_idp_hint`; Auth0 deployments can instead configure the corresponding `connection` hint. Only configured connections appear in the chooser. An empty list uses the issuer's own login page.

The login request may include `returnTo` to restore a chat or host-app page. Destinations are restricted to the configured frontend origin and exact `origins` allowlist. Arbitrary redirect URIs, protocol overrides and user-controlled broker parameters are rejected. Logout revokes the local Tamizhi session; it deliberately does not sign the user out of every application at their identity provider. Account linking belongs to the broker; Tamizhi never merges accounts on matching email addresses.

App sign-in identifies a user. It **does not grant AI inference credits**. See [account and quota integration status](docs/tamizhi-auth.md) for provider-account OAuth constraints and remaining registrations.

## Google account model access

Gemini supports a separate **Connect Google account** action in AI settings when the server's optional `geminiOAuth` registration is configured. Users choose an authorized Google Cloud quota project and consent to model API access; they do not need to enter an API key. Access/refresh tokens are encrypted, refreshed automatically and removed on disconnect. See [setup and authorization boundaries](docs/tamizhi-auth.md#enable-gemini-account-connections) and `config/tamizhi.oauth.example.json`. This flow does not import consumer subscription credits.

## Bring your own key (BYOK)

Users sign in, open **Tuning → AI settings**, select a provider, enter their API key and choose **Test & save key**. The server discovers that account's available models before saving. App and embed refresh their model/audio capabilities together. Keys never enter local storage or provider-settings responses; removing or replacing a key cancels that user's active runs.

Supported presets are OpenAI, Anthropic, Gemini, DeepSeek, Kimi and xAI. Anthropic and Gemini use their native official Go SDKs; the others use the official OpenAI-compatible client. Endpoints are trusted server definitions, not user-supplied URLs. Audio is available when the connected OpenAI account exposes transcription/speech models. The disconnected demo picker remains a set of sample presets; it does not promise vendor model availability.

Saved keys are encrypted with AES-256-GCM and bound to the authenticated owner/provider. Set server-only `TAMIZHI_ENCRYPTION_KEY` to a base64-encoded random 32-byte key for stable environments. Without it, the server creates `.vezham/tamizhi/credential-key` with owner-only permissions. Back up this master key separately with the database: losing it prevents decryption of saved provider keys. Never place it in public frontend variables. Operator-configured provider credentials remain optional.

## Plugins and execution

The runtime uses the pinned community Go Cordis port behind Tamizhi registration methods. Lifecycle tests cover dependencies, unload cleanup and reactivation. This is a Go implementation inspired by DeepSeek Harness's composition philosophy, not a wrapper around the DSH runtime.

- `provider.<id>`: OpenAI-compatible chat completions using the official Go SDK, incremental text/tool calls, bounded retries and usage accounting. Multiple provider instances and model catalogs are supported. Fast mode selects a server-declared fast model in the selected provider.
- `memory`: user-owned persistent facts, keyword retrieval and remember/recall tools.
- `http`: guarded public HTTP fetch; DNS results, redirect destinations and private/reserved addresses are checked.
- `workspace`: list/read/write/search inside explicitly assigned per-user roots using `os.Root`. Symlink escapes and path traversal are blocked. Enable the `workspace` profile and configure `workspaceRoots` by authenticated principal ID.
- `tool.navigate`: emits a typed local navigation request; the browser displays a link.
- `tool.delegate_task`: child runs with inherited ownership/permissions, cancellation, depth and global concurrency limits. Child events and approval requests flow through the parent's durable stream.
- `mcp.<name>`: official MCP Go SDK; configured trusted Streamable HTTP or stdio servers and an explicit tool allowlist. All MCP tools require external-action permission. Commands/URLs are administrator configuration, never model-supplied executable strings.

Compiled plugin enablement can change through the admin API or watched JSON configuration. Unloading a provider cancels dependent active runs. Filesystem/MCP resources close on unload. Authentication, database and provider definition changes require a controlled restart. Persisted admin enablement takes precedence over initial config; subsequent file edits apply their explicit enablement values.

Tools declare read/write/external risk and JSON Schema inputs. `ask` requests approval for every tool. `safe` permits read tools and requests approval for other actions. `full` only bypasses approvals when the server also enables `allowFullAccess`; the default is false. Arguments, output sizes, execution steps, parallel tools, run duration and pending approvals are bounded. Tool attempts/results and run events are recorded; interrupted attempts are never silently retried after restart.

## Authentication and deployment

OIDC uses provider discovery, signed ID-token issuer/audience/expiry verification, PKCE S256, nonce and one-use state tied to an HttpOnly browser cookie. Session tokens are opaque and stored as SHA-256 hashes. Logout revokes the server session. API bearer credentials also bind to an explicit principal. Every conversation, upload, memory entry, replay and approval checks that identity.

For browser OIDC configure `auth.issuer`, `clientId`, `clientSecretEnv`, `redirectURL`, `frontendURL`, `secureCookies: true` and optional exact `adminSubjects`. Register the callback `/api/tamizhi/auth/callback` with your identity provider. Deploy frontend and API on the same site (or use a same-origin reverse proxy) for the Lax session-cookie policy. Configure exact frontend `origins`; credentialed CORS does not accept wildcards.

Neon Postgres is the deployment database and the default server configuration. `databaseDriver: "pgx"` selects the existing Go PostgreSQL adapter; `databaseURLEnv: "DATABASE_URL"` reads its connection string from the server environment. Missing credentials stop startup instead of silently using SQLite.

Copy the connection string from Neon Console → Connect, keeping its TLS/channel-binding parameters. Use the pooled (`-pooler`) URL for normal API traffic. Keep a direct URL for tools that require a direct connection, such as `pg_dump` or migration tools using session-level features. The current startup schema creation uses ordinary SQL and does not require a separate migration connection. See [Neon's Go guide](https://neon.com/docs/guides/go) and [connection pooling documentation](https://neon.com/docs/connect/connection-pooling).

```sh
export DATABASE_URL='postgresql://ROLE:PASSWORD@YOUR-POOLER-HOST/neondb?sslmode=require&channel_binding=require'
```

Do not add this URL to any `V_` frontend variable or committed JSON configuration. SQLite remains available only when explicitly selected for local development or tests (`databaseDriver: "sqlite"`). Run **one API process per database**: run coordination and approval waits are process-local, and startup recovery marks unfinished runs interrupted. A distributed lease/worker scheduler is a separate scaling project. Mount persistent storage and back it up; deletion hides conversations and retains their audit history.

`Dockerfile` builds a non-root, CGO-free API image. `compose.yaml` mounts a read-only config and persistent data volume. Production should use a TLS reverse proxy, OIDC and server-managed secret injection. Do not expose stdio MCP commands or shared filesystem roots to untrusted configuration authors.

## Protocol and synchronization

The route prefix is `/api/tamizhi`. Generated route and schema documentation is in `api/tamizhi/openapi.yaml`.

- Chat streams persist sequenced SSE events before delivery. A dropped HTTP connection leaves its run alive; reconnect with `GET /runs/:id/events?after=N` or `Last-Event-ID`. Stop uses `POST /runs/:id/cancel`.
- Conversations support list/search/cursors, rename/delete, feedback/tasks, branching, checkpoints and restore. Writes use optimistic versions; conflicting active operations return 409.
- Text/image uploads are owner-scoped. PDFs are extracted as text (no OCR); text is capped at 100 KB and uploads at 10 MB. Image input requires a model declaring `image` modality. PDF extraction runs in-process; only use trusted documents until a separate extraction worker is deployed for hostile-document workloads.
- Transcription and speech are enabled by provider audio model configuration. Models without audio support expose those capabilities as false.
- Compaction preserves the durable transcript and adds a model-generated summary for older context. Manual checkpoints preserve transcript and summary. Retrieval is keyword-based, not vector search.

```sh
pnpm nx run tamizhi-contracts-go:generate
scripts/sync-tamizhi-contracts /path/to/vx-fe-pkgs-timeline
scripts/sync-tamizhi-contracts /path/to/vx-fe-pkgs-timeline --check
```

Generated backend `api.ts`, `schemas.json` and `openapi.yaml` come from Go DTOs. The sync script copies `api.ts` into the frontend source and copies the generated YAML directly into `packages/tamizhi/contracts/openapi.yaml` as a repository reference. Both copies are checked byte-for-byte for drift. Generated schema and OpenAPI files are excluded from Prettier so formatting cannot invalidate generator checks. The frontend keeps runtime Zod validation for requests, stored histories and stream events. Its client integration test starts the actual Go binary and a local model endpoint to verify both sides together.

Set the **public** frontend build variable `V_TAMIZHI_API_URL=https://api.example.com/api/tamizhi`; no SSR runtime or secret frontend variables are required. Both app modes consume `@vezham/tamizhi-client` and `@vezham/tamizhi-ui`. Control Center continues to own theming.

## Verification

```sh
pnpm nx run-many -t build,test,lint,typecheck --parallel=1
pnpm nx run tamizhi-contracts-go:check-contracts
pnpm nx run @vx:format-check
# Optional local PostgreSQL integration; CI provides a database service.
TAMIZHI_TEST_POSTGRES_URL='postgres://user:password@localhost/tamizhi?sslmode=disable' pnpm nx run tamizhi-core-go:test
```

In the frontend repository:

```sh
TAMIZHI_BACKEND_DIR=/path/to/vx-be-kit-lab pnpm nx run tamizhi-client:test --skip-nx-cache
pnpm nx run-many -t test,typecheck,lint -p tamizhi-contracts,tamizhi-client,tamizhi-ui,tamizhi --parallel=1
pnpm nx run tamizhi:build
TAMIZHI_BACKEND_DIR=/path/to/vx-be-kit-lab pnpm nx run tamizhi-ui:browser-test
```

The local suite uses temporary databases, a signed test identity provider, a real MCP HTTP server and a mocked model endpoint; it does not send paid model requests. The Neon integration suite passed on an isolated temporary branch. Real paid vendor requests require a user-supplied BYOK key. Browser tests use a signed local OIDC issuer and local model/audio fixtures. Docker, deployment and CI verification are excluded from this work; no workflows were triggered.

## Local product state

Tamizhi uses `.vezham/tamizhi/`, relative to the process working directory. Explicit SQLite development defaults to `.vezham/tamizhi/data/tamizhi.sqlite`; the local credential-encryption key defaults to `.vezham/tamizhi/credential-key`. Neon remains the production database. Configured database paths, `CredentialKeyFile` and the server-only `TAMIZHI_ENCRYPTION_KEY` retain their existing override behavior.

## Upstream review history

The [Tamizhi upstream sync handbook](vx/handbooks/tamizhi-upstream-sync.md)
records DSH snapshots, decisions, baseline commits, verification and deferred
Search/Fetch and Team work. Keep it updated with backend and frontend commit IDs
when an upstream review is implemented.

MCP integrations are optional by default. Set `required: true` on an MCP config
entry to make activation mandatory at startup. Other compiled plugins remain
required. Optional activation failures are logged and exposed as failed plugins
without blocking the API. Invalid registrations still stop startup. Admins can
retry failed activation in AI settings. Plugin enablement is saved before runtime
application; saved intent and actual runtime status are reported separately.

### Search source

`web_search` uses DeepSeek Search with the initiating owner's encrypted DeepSeek
API key. In AI settings, select DeepSeek, test and save a key, then explicitly
turn on **Leo web search**. Connecting or replacing a key leaves search off.
Disconnecting the provider removes search access. Search never uses another
owner's key or falls back to a system key.

One successful search costs $0.75 per 1,000 calls at the published November 2026
rate. Check https://api-docs.deepseek.com/quick_start/pricing/ for current pricing.
This integration requests six ranked results with `need_content: false`, then
uses the existing public HTTPS open/read tools for page retrieval. It does not
call DeepSeek Fetch or an LLM. Search results alone are not source evidence:
only content read from source pages supports citations. Direct page reads may
fail when sites require JavaScript, login or verification; these failures remain
visible. There is no browser or Google HTML search fallback.

The adapter does not automatically retry paid search calls. It preserves result
order, removes duplicate/unsafe URLs, bounds responses to 512 KiB and uses a
128-character Unicode-safe query limit. Credentials are sent only to the fixed
DeepSeek HTTPS search endpoint; redirects are rejected. Missing keys, disabled
search, authentication failures, insufficient balance and rate limits produce
actionable errors without exposing provider response bodies or secrets.

The authenticated `PUT /api/tamizhi/providers/{id}/search` endpoint accepts
`{"enabled": true}` or `{"enabled": false}` and returns credential-free provider
settings. It supports DeepSeek only. `WebOptions.Search` remains an injection
point for other trusted adapters and deterministic tests.
