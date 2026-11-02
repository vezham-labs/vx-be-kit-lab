# Tamizhi Go implementation ledger

Backend repository: vx-be-kit-lab. Frontend integration: vx-fe-pkgs-timeline.
Checked entries below are implemented and locally verified. Deployment checks are recorded separately; they are not implied by a checked implementation item.

## Acceptance scope

- [x] Pinned Go toolchain; Nx generation/build/test/lint; deployment assets exist but execution is excluded.
- [x] Evaluated Go Cordis adapter; plugin services, dependencies, cleanup, configuration/profiles, admin management.
- [x] Generated Go/TypeScript API contracts and repeatable frontend sync with drift checks.
- [x] Authenticated multi-user sessions; browser login/session integration and API credentials.
- [x] Real provider streaming, catalog/model routing, Fast mode, cancellation, retries and usage accounting.
- [x] Agent tool loop; validated inputs; explicit approvals; audit events; bounded concurrency.
- [x] Durable session/event log; list/search/rename/delete, feedback/tasks, branch and checkpoints.
- [x] Replay/reconnect and interrupted-run recovery without silently replaying unsafe actions.
- [x] Text/image/PDF attachments, transcription and speech via capability plugins.
- [x] Workspace files/search/navigation and HTTP tools with declared execution boundaries.
- [x] MCP integration with configurable trusted servers.
- [x] Durable memory/retrieval and context compaction.
- [x] Bounded subagent delegation with inherited ownership and permission policy.
- [x] HeroUI standalone/embed integration for all exposed capabilities; Control Center theme preserved.
- [x] Cross-repository contract tests, backend integration/race tests, frontend checks and static build.
- [x] Document any verification that requires unavailable provider or identity-provider credentials.

## Decisions

- The Go backend owns runtime/provider secrets; frontend applications remain static/prerendered.
- Existing TypeScript UI/client contracts form the migration baseline; the TypeScript backend is a prototype, not the production runtime.
- Reuse mature packages where they fit. Framework-specific code stays behind Tamizhi interfaces.
- No blanket feature-completion claim: only checked, verified capabilities count as done.

## Local verification record

- Go API/contracts/core: Nx build, race-enabled tests, vet and generated-contract drift checks passed. Provider tests exercise native Anthropic/Gemini tool round trips, encrypted BYOK settings, key rejection and owner isolation. PostgreSQL tests are opt-in via `TAMIZHI_TEST_POSTGRES_URL`.
- Frontend: tests passed across contracts, client, UI, standalone app and the retained TypeScript prototype; affected typechecks and lint passed.
- Cross-repository test: the TypeScript client launched the actual Go API binary with a temporary SQLite database and a local model endpoint. Streaming/replay, authentication, history, feedback, branching, checkpoints, uploads, search, memory and plugin changes passed.
- Static/prerendered standalone builds passed with and without the API URL configured. The API URL participates in the Nx build cache key.
- Backend formatting and byte-for-byte frontend contract synchronization passed.

## Deployment verification remaining

- Real vendor requests require a user-provided BYOK key. No operator provider key is required; Settings tests discover the account models before saving. Local SDK streaming/image/audio fixtures passed; no paid vendor requests were made.
- [ ] Validate browser login against the chosen deployed OIDC provider. The signed local OIDC fixture passed PKCE, nonce/state, session ownership and logout checks.
- [x] Full Neon schema/CRUD/ownership/cursor/replay/attachment and HTTP streaming tests passed on a temporary child branch. The branch was deleted; main was unchanged. Fixed PostgreSQL cursor type inference for millisecond timestamps.
- Excluded by user: Docker execution, deployment and CI verification.
- [x] Local Chromium checks passed for signed OIDC login, encrypted BYOK settings, image chat, actual native audio decoding/playback, shared embed and compact failure feedback. Native microphone capture is unverified: this host returns `NotSupportedError` from `getUserMedia`, including outside Tamizhi. The suite records that environment limitation and asserts useful UI feedback. Wire-level transcription tests passed against the local provider fixture.

No CI workflows were triggered, no deployment was performed, and no commits or pushes were made. One API process per database is the supported deployment model; distributed workers are outside this implementation scope.

Neon Postgres is the selected production database. Default server configuration resolves `DATABASE_URL` from its environment, and explicitly configured SQLite fixtures remain supported.

## Neon organization setup

- Organization: `org-summer-base-09791622`.
- Project: `tamizhi` / `late-flower-50992894`, Singapore, Postgres 18, branch `main`.
- CLI authenticated; project linked; `.env.local` populated and git-ignored with mode 0600.
- Official Neon/Postgres/branching skills installed; project-scoped Codex MCP configured with OAuth.
- Both pooled and direct URLs verified with live read-only Go/pgx queries. Full schema/CRUD integration subsequently passed in an isolated child branch, which was deleted after testing. The main branch was unchanged.
- Restart Codex in the backend workspace to load the MCP server and newly installed skills.

## BYOK implementation

- Users connect OpenAI, Anthropic, Gemini, DeepSeek, Kimi or xAI in Tuning → AI settings. Native official Anthropic/Gemini SDKs and OpenAI-compatible providers discover account models.
- Provider keys are encrypted with AES-256-GCM, owner/provider-bound, and excluded from responses and browser storage. Master key is server-only `TAMIZHI_ENCRYPTION_KEY` (base64 32 bytes) or a persistent mode-0600 local key file. Back up the master key separately.
- App/embed subscribe to provider changes and refresh capabilities. Key removal/replacement cancels the owner's active runs. Voice features depend on discovered OpenAI audio models.
- Backend build/race/vet and SDK tool-correlation tests passed. Replacing a file input's value no longer drops queued attachment selections.

## SSO follow-up

Visible shared account controls, configured social/enterprise connection chooser, public session status and allowlisted post-login destination restoration are implemented. External broker/client registrations are not configured by the example template. See `tamizhi-auth.md` for the distinction between Tamizhi identity login and provider model-use authorization; no consumer credit-sharing integration is claimed complete.

## Gemini account OAuth

Implemented an optional, registered Google web-client account connection alongside BYOK. Users select an authorized quota project; tokens stay owner-bound and encrypted, refresh automatically with serialized rotation, and are fenced against disconnect/replacement. The callback uses PKCE, short-lived HttpOnly state cookies, encrypted pending state, one-use exchange and allowlisted destinations. Images, tool IDs/thought signatures and usage stream over the documented REST endpoint; the selected project supplies API permissions/quota/billing.

Backend race tests cover refresh rotation/concurrency, owner isolation, callback expiry/forgery/account switching, late grant rejection and disconnect/revocation. Seven Chromium scenarios passed, including account authorization, a key-free Gemini reply, token absence from browser storage and removal of disconnected models. These use local provider fixtures; real Google account consent still requires the operator's client registration. Hosted ChatGPT plan-sharing remains gated on provider access/registration, and unsupported consumer/CLI login surfaces are not advertised.
