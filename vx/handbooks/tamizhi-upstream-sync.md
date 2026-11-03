# Tamizhi upstream review and sync history

This handbook records DeepSeek Harness reviews and the behaviors Tamizhi adopts.
Tamizhi has an independent Go backend and static/prerendered HeroUI clients.
Upstream commits are design references, not patches applied to a DSH dependency.
The TypeScript core package remains a comparison prototype.

## TODO checklist

Checked items describe implemented and locally verified work. Unchecked items
are pending tracking steps or explicitly deferred work; they do not imply a
commitment to port every DSH subsystem.

### Completed in this review

- [x] Record upstream snapshot `8bd073392eef6f54e921a093b95076248eb08cbb`
      and the reviewed commit inventory.
- [x] Isolate optional MCP activation failures while preserving required-plugin
      and invalid-registration startup failures.
- [x] Expose desired plugin enablement, actual activation status, safe errors,
      and explicit retry in settings.
- [x] Persist plugin intent before applying runtime changes; leave runtime
      unchanged when persistence fails.
- [x] Preserve confirmed provider saves/removals when subsequent status refresh
      fails; retry reads without resubmitting keys.
- [x] Regenerate and sync Go-owned API types, JSON Schema, OpenAPI YAML, and
      browser validation.
- [x] Verify Go race/vet checks, frontend tests/typecheck/lint, operator retry
      browser scenarios, and local React Doctor diagnostics.

### Tracking and next review

- [ ] Record the actual backend implementation commit SHA after committing.
- [ ] Record the actual frontend implementation commit SHA after committing.
- [ ] Perform a broader architecture/parity audit before claiming full DSH
      alignment; document which core responsibilities should become plugins.
- [ ] At the next upstream review, compare from the recorded snapshot through
      the new head, include merged changes, and append a new ledger entry.

### Deferred capabilities

- [ ] Add a dedicated web-search plugin with independent Search/Fetch provider
      selection, readiness, credentials, and permission tests.
- [ ] Add optional Agent Team mode with durable shared task ownership,
      dependencies, root-mode initialization, cancellation, and restart recovery.
- [ ] Evaluate extracting orchestration, persistence, and authentication behind
      plugin interfaces while preserving mandatory service boundaries.

### Revisit only when the architecture needs it

- [ ] If adding lazy/deferred tool discovery, review tool-addition/removal
      history rebuilding and provider rejection handling.
- [ ] If adding plugin-owned durable projections, preserve released projection
      state through checkpoints, unload, and restart.
- [ ] Before changing the Go Cordis dependency, review that port's own changes
      and rerun lifecycle/dependency/disposal tests; DSH JavaScript version
      changes alone do not require an upgrade.

## Review ledger

| Reviewed on | Upstream snapshot                                                                                                                                                     | Review window                                                                        | Tamizhi baseline                                                                             | Implementation status                                                                         |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| 2026-11-04  | [`8bd073392eef6f54e921a093b95076248eb08cbb`](https://github.com/deepseek-ai/deepseek-harness/commit/8bd073392eef6f54e921a093b95076248eb08cbb), master / 0.2.2-alpha.3 | Commits returned by `git log --no-merges --since=2026-11-02T00:00:00Z origin/master` | FE `4927d1a7e663c312bf8f35f7c9b9120d79f8ec41`; BE `8961d7648a57cff9c21940e80a29a5d103625f96` | Implemented in working trees; implementation SHA is available after the changes are committed |

Do not invent an implementation SHA before committing. Keep this entry with the
implementation changes. Use `git log --follow -- vx/handbooks/tamizhi-upstream-sync.md`
in this backend repository to locate the commit recording each review, and
`git log -- packages/tamizhi/ui/src/lib/backend-settings.tsx packages/tamizhi/ui/src/lib/provider-settings.tsx`
in the frontend for the matching UI changes. Record both resulting SHAs
in the ledger when the repositories' commits are available. Neither repository
is committed or pushed automatically by this review workflow.

## Decisions for the 2026-11-04 review

| Upstream reference                                                                                                                                                                                                                                                                            | Decision                                                       | Tamizhi implementation or reason                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`6e77ea0131`](https://github.com/deepseek-ai/deepseek-harness/commit/6e77ea0131), optional client-plugin boot failures                                                                                                                                                                       | Adopt the failure-isolation principle for backend integrations | MCP plugins are optional by default. Core, configured provider, and workspace plugins remain required. Set an MCP entry's `required: true` to make activation mandatory. Only activation failures are isolated; invalid or duplicate registrations remain fatal. Failed integrations appear in plugin settings and support explicit retry.                                                                                                                                                                 |
| [`224fe9a658`](https://github.com/deepseek-ai/deepseek-harness/commit/224fe9a658), committed versus uncertain settings saves; [`eb5b32c9c9`](https://github.com/deepseek-ai/deepseek-harness/commit/eb5b32c9c9), apply-failure recovery                                                       | Adopt                                                          | Plugin settings persist desired enablement before applying it. A persistence failure leaves the current runtime intact; a successful save with failed activation returns the saved state plus `status: failed` and a safe error. Retry repeats activation explicitly. Provider-key save/removal confirmation is preserved when a later model/status refresh fails; Refresh status retries reads without resending credentials. Unconfirmed mutations request reconciliation rather than automatic retries. |
| [`3859913c68`](https://github.com/deepseek-ai/deepseek-harness/commit/3859913c68), separate Search and Fetch                                                                                                                                                                                  | Deferred feature                                               | Tamizhi has a bounded public `http_fetch` tool. Add search and independent provider readiness/credentials when implementing web search. Do not silently grant search-account access or import subscription credits.                                                                                                                                                                                                                                                                                        |
| [`e180fce05a`](https://github.com/deepseek-ai/deepseek-harness/commit/e180fce05a), [`dd9719f67a`](https://github.com/deepseek-ai/deepseek-harness/commit/dd9719f67a), Team mode and initialization                                                                                            | Deferred feature                                               | Existing bounded delegation remains. A future team plugin needs durable task ownership, dependencies, root-mode initialization, cancellation and recovery tests. Do not claim task-board parity.                                                                                                                                                                                                                                                                                                           |
| [`1f3bc8b22b`](https://github.com/deepseek-ai/deepseek-harness/commit/1f3bc8b22b), [`be6ed7f0aa`](https://github.com/deepseek-ai/deepseek-harness/commit/be6ed7f0aa), [`624c3e91a9`](https://github.com/deepseek-ai/deepseek-harness/commit/624c3e91a9), tool histories and deferred requests | No direct port                                                 | Tamizhi sends the complete active tool list on each step and does not implement DSH's deferred-tool update projection. Revisit if lazy tool discovery or incremental declarations are introduced.                                                                                                                                                                                                                                                                                                          |
| [`eb1713f856`](https://github.com/deepseek-ai/deepseek-harness/commit/eb1713f856), released projection checkpoints                                                                                                                                                                            | No direct port                                                 | Current checkpoints store complete conversation messages and summary. There is no plugin-owned projection-registration lifecycle. Revisit before adding durable plugin-specific projections.                                                                                                                                                                                                                                                                                                               |
| [`05b1cae8db`](https://github.com/deepseek-ai/deepseek-harness/commit/05b1cae8db), Cordis/vendor prereleases                                                                                                                                                                                  | No automatic upgrade                                           | Production uses a separately pinned Go Cordis port. JavaScript prereleases are not Go upgrades; the old TypeScript core is not the production runtime.                                                                                                                                                                                                                                                                                                                                                     |
| Desktop updater, CLI module aliases, Python runtime packaging, scheduled tasks, CUA signing and shared-checkout policy changes                                                                                                                                                                | No direct port in this review                                  | These DSH subsystems have no equivalent implementation in Tamizhi. Reassess when adding those capabilities. The reviewed commit inventory below preserves their history.                                                                                                                                                                                                                                                                                                                                   |

## Contract and behavior details

- Go owns `PluginInfo`: `required` reports startup policy; `enabled` reports desired
  enablement. `status` reports runtime state independently. `error` is a safe
  activation message; private startup details stay in server logs.
- Admin-only plugin PATCH persists intent before runtime mutation. Failed
  persistence returns `setting_unconfirmed`; the UI reads plugin status again
  and asks the operator to refresh before retrying. Successful persistence with
  failed activation returns the plugin list with failed state, avoiding a false
  claim that the setting was not saved.
- A failed optional plugin has no active fiber. Retrying its enabled state is
  permitted; disabling it clears the failure. No automatic activation retries
  or secret-bearing error strings are introduced in API responses.
- Auth, database startup, required integrations, and invalid registrations keep
  their existing startup failure behavior. Optional failures never weaken tool
  permission checks or user boundaries.
- Generated `api.ts`, JSON Schema, and OpenAPI YAML must be regenerated and synced
  together with browser runtime validation after a contract change.

## Verification record

The review adds Go regression coverage for optional versus required MCP startup,
explicit activation retry, preserved saved intent, sanitized error state, and
persistence failures that leave the runtime unchanged. Frontend tests cover
provider saves/removals followed by failed read refreshes, with no duplicate
credential submission. The local browser fixture includes an optional plugin
that fails its first activation, allowing an admin to retry it and reopen settings.

Run backend `tamizhi-core-go:test` (race enabled), `tamizhi-core-go:lint`, contract
checks, and frontend `tamizhi-ui:test`, `tamizhi-ui:typecheck` plus the browser
scenario `optional plugin activation failure remains visible and explicitly retryable`.
Use local React Doctor diagnostics with external scoring disabled, as requested.

### Results recorded for this entry

- Backend: Go race tests, vet and core build passed. Contract generator tests and
  drift checks passed, including the documented plugin 503 response.
- Frontend: 49 UI tests, 14 contract tests and 5 client tests passed; the separate
  live Go client integration test remains opt-in and was skipped in the unit run.
- Forced UI typecheck and changed-file ESLint passed.
- Chromium: optional plugin retry/reopen and existing backend settings actions
  both passed against local Go/OIDC/provider fixtures.
- Local React Doctor: 0 errors, 0 warnings; external scoring was not invoked.
- Direct backend-to-frontend TypeScript/OpenAPI synchronization passed `--check`.

## Next review procedure

1. Fetch upstream without switching or modifying its checkout. Record its full SHA.
2. Compare from the previous recorded snapshot, including merged changes; append
   a ledger entry rather than replacing review history.
3. Map each relevant upstream change to a Tamizhi behavior. Record adopted,
   deferred, already covered, or inapplicable decisions with commit links.
4. Implement against Go contracts and HeroUI components; regenerate/sync the
   wire artifacts and add failure-focused tests where behavior changes.
5. Record verification and implementation SHAs once commits exist. Keep deferred
   Search/Fetch and Team work visible until implemented or explicitly declined.

## Upstream commit inventory

The following 72 commits were returned for the recorded review window. This is
a dated log inventory, not a claim that every upstream subsystem has been ported.
The decision table above records what Tamizhi adopts.

| Upstream commit                                                                   | Subject                                                                                                                                                                                                                             |
| --------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`24db102f15`](https://github.com/deepseek-ai/deepseek-harness/commit/24db102f15) | release(dsh): align master packages with 0.2.2-alpha.3                                                                                                                                                                              |
| [`05b1cae8db`](https://github.com/deepseek-ai/deepseek-harness/commit/05b1cae8db) | release(vendor): cordis 4.0.6-alpha.2, cosmokit 1.8.7-alpha.2, group 1.0.6-alpha.2, hmr 1.0.21-alpha.2, include 1.0.11-alpha.2, loader 1.0.7-alpha.2, logger-console 1.0.6-alpha.2, schemastery 3.18.6-alpha.2, timer 1.1.8-alpha.2 |
| [`8c523d9be1`](https://github.com/deepseek-ai/deepseek-harness/commit/8c523d9be1) | refactor(team): remove startup step-history guard                                                                                                                                                                                   |
| [`f26f308e5e`](https://github.com/deepseek-ai/deepseek-harness/commit/f26f308e5e) | Merge pull request #7768 from deepseek-harness/fix/goal-shutdown-pause                                                                                                                                                              |
| [`385879882c`](https://github.com/deepseek-ai/deepseek-harness/commit/385879882c) | test(mcp): refresh the template snapshot for master's read and web_search guidance                                                                                                                                                  |
| [`dd9719f67a`](https://github.com/deepseek-ai/deepseek-harness/commit/dd9719f67a) | fix(team): initialize configured mode before child activation ownership                                                                                                                                                             |
| [`1c78d475bf`](https://github.com/deepseek-ai/deepseek-harness/commit/1c78d475bf) | release(dsh): 0.2.2-alpha.3                                                                                                                                                                                                         |
| [`0753977b60`](https://github.com/deepseek-ai/deepseek-harness/commit/0753977b60) | test(desktop): assert structured settings failures                                                                                                                                                                                  |
| [`3859913c68`](https://github.com/deepseek-ai/deepseek-harness/commit/3859913c68) | feat(web): 网页搜索与获取设置页分开选择 Search 与 Fetch，Search 默认改为 DeepSeek Search API                                                                                                                                        |
| [`5b6366f5bf`](https://github.com/deepseek-ai/deepseek-harness/commit/5b6366f5bf) | Type the canonical CLI module import                                                                                                                                                                                                |
| [`cbe275fe99`](https://github.com/deepseek-ai/deepseek-harness/commit/cbe275fe99) | Keep embedded CLI imports on the carrier module tree                                                                                                                                                                                |
| [`8632896e84`](https://github.com/deepseek-ai/deepseek-harness/commit/8632896e84) | Resolve Windows CLI entry aliases before loading runtime modules                                                                                                                                                                    |
| [`d7ca2e0ac2`](https://github.com/deepseek-ai/deepseek-harness/commit/d7ca2e0ac2) | fix(desktop): keep configuration recovery dialog interactive                                                                                                                                                                        |
| [`06bf7ce024`](https://github.com/deepseek-ai/deepseek-harness/commit/06bf7ce024) | Revert "fix(settings): show saved API keys and preserve environment detection…"                                                                                                                                                     |
| [`f07af538f8`](https://github.com/deepseek-ai/deepseek-harness/commit/f07af538f8) | fix(schedule): initialize stored task timing controls (#7704)                                                                                                                                                                       |
| [`a3f6f94c1e`](https://github.com/deepseek-ai/deepseek-harness/commit/a3f6f94c1e) | test(snapshot): refresh fixtures for the 10-query web_search default                                                                                                                                                                |
| [`cc673ec174`](https://github.com/deepseek-ai/deepseek-harness/commit/cc673ec174) | fix(team): identify the coordinator as the root agent                                                                                                                                                                               |
| [`e180fce05a`](https://github.com/deepseek-ai/deepseek-harness/commit/e180fce05a) | feat(team): enable new root Team mode through Cordis configuration                                                                                                                                                                  |
| [`f7c184ffd1`](https://github.com/deepseek-ai/deepseek-harness/commit/f7c184ffd1) | fix(team): restore the original shared-task policy wording                                                                                                                                                                          |
| [`0535b85a1d`](https://github.com/deepseek-ai/deepseek-harness/commit/0535b85a1d) | fix(desktop): recover update settings under mandatory policy                                                                                                                                                                        |
| [`48e21ac6d7`](https://github.com/deepseek-ai/deepseek-harness/commit/48e21ac6d7) | fix(team): guide agents to create and track shared tasks                                                                                                                                                                            |
| [`eb5b32c9c9`](https://github.com/deepseek-ai/deepseek-harness/commit/eb5b32c9c9) | fix(desktop): report and recover saved channel apply failures                                                                                                                                                                       |
| [`224fe9a658`](https://github.com/deepseek-ai/deepseek-harness/commit/224fe9a658) | fix(desktop): distinguish committed and uncertain settings saves                                                                                                                                                                    |
| [`eb1713f856`](https://github.com/deepseek-ai/deepseek-harness/commit/eb1713f856) | fix(session-projection): keep released units in checkpoints (#7754)                                                                                                                                                                 |
| [`6e77ea0131`](https://github.com/deepseek-ai/deepseek-harness/commit/6e77ea0131) | feat(web): boot past failed optional client plugins (#7733)                                                                                                                                                                         |
| [`9cd3fbec45`](https://github.com/deepseek-ai/deepseek-harness/commit/9cd3fbec45) | revert(webworker): drop the cross-spawn refusal from the preview worker                                                                                                                                                             |
| [`d721353f86`](https://github.com/deepseek-ai/deepseek-harness/commit/d721353f86) | feat(forge): unify plugin entry for reviews and worktrees (#7721)                                                                                                                                                                   |
| [`63026c7a5c`](https://github.com/deepseek-ai/deepseek-harness/commit/63026c7a5c) | feat(client): 左侧栏纵向空间优化，工作区滚动吸顶优化 (#7337)                                                                                                                                                                        |
| [`624c3e91a9`](https://github.com/deepseek-ai/deepseek-harness/commit/624c3e91a9) | fix(llm-deepseek): expose all-deferred request failures                                                                                                                                                                             |
| [`f54967a90d`](https://github.com/deepseek-ai/deepseek-harness/commit/f54967a90d) | Merge pull request #7543 from deepseek-harness/feat/sandbox-policy-caps                                                                                                                                                             |
| [`30bf5733d6`](https://github.com/deepseek-ai/deepseek-harness/commit/30bf5733d6) | refactor(mcp-manager): publish the manager as ctx.mcpManager                                                                                                                                                                        |
| [`4658cba7e0`](https://github.com/deepseek-ai/deepseek-harness/commit/4658cba7e0) | Merge pull request #7564 from deepseek-harness/fix/http-proxy-http1                                                                                                                                                                 |
| [`bedcb559ac`](https://github.com/deepseek-ai/deepseek-harness/commit/bedcb559ac) | test(snapshot): refresh the ptc-script-imports system prompt (#7738)                                                                                                                                                                |
| [`a5c44b9384`](https://github.com/deepseek-ai/deepseek-harness/commit/a5c44b9384) | test(llm-deepseek): refresh snapshots for root agent identity                                                                                                                                                                       |
| [`1f3bc8b22b`](https://github.com/deepseek-ai/deepseek-harness/commit/1f3bc8b22b) | fix(llm): rebuild tool history for additions to an empty set                                                                                                                                                                        |
| [`523e741a31`](https://github.com/deepseek-ai/deepseek-harness/commit/523e741a31) | test(llm-deepseek): cover all-deferred requests and separate tool updates                                                                                                                                                           |
| [`9d53909bcf`](https://github.com/deepseek-ai/deepseek-harness/commit/9d53909bcf) | docs(llm): clarify deferred tool activation and placeholder effects                                                                                                                                                                 |
| [`be93b94e6e`](https://github.com/deepseek-ai/deepseek-harness/commit/be93b94e6e) | docs(notes): state that every tool removal rebuilds the baseline                                                                                                                                                                    |
| [`be6ed7f0aa`](https://github.com/deepseek-ai/deepseek-harness/commit/be6ed7f0aa) | fix(llm): rebuild addition-only history on every removal                                                                                                                                                                            |
| [`f8217a4fe1`](https://github.com/deepseek-ai/deepseek-harness/commit/f8217a4fe1) | fix(llm): rebuild addition-only baselines after tool removals                                                                                                                                                                       |
| [`156dad153d`](https://github.com/deepseek-ai/deepseek-harness/commit/156dad153d) | fix(llm-deepseek): anchor all-deferred tools with a placeholder                                                                                                                                                                     |
| [`07f2b259e7`](https://github.com/deepseek-ai/deepseek-harness/commit/07f2b259e7) | ci: run the merge checks on release branches, and fix the ptc-script-imports fixture (#7732)                                                                                                                                        |
| [`ec7a4e9df5`](https://github.com/deepseek-ai/deepseek-harness/commit/ec7a4e9df5) | fix(creation-recommendations): show Cancel beside the location while creating                                                                                                                                                       |
| [`86e2cc8277`](https://github.com/deepseek-ai/deepseek-harness/commit/86e2cc8277) | feat(schedule): prefill task creation on release 0.2.2 (#7702)                                                                                                                                                                      |
| [`88b01c24ea`](https://github.com/deepseek-ai/deepseek-harness/commit/88b01c24ea) | fix(desktop): discard stale channel check prompts and align error copy                                                                                                                                                              |
| [`d3f7b8abe7`](https://github.com/deepseek-ai/deepseek-harness/commit/d3f7b8abe7) | Merge pull request #7701 from deepseek-harness/fix/empty-session-sidebar-0.2.2                                                                                                                                                      |
| [`e0129468c3`](https://github.com/deepseek-ai/deepseek-harness/commit/e0129468c3) | fix(creation-recommendations): offer only Undo on installed page cards                                                                                                                                                              |
| [`ec587a015d`](https://github.com/deepseek-ai/deepseek-harness/commit/ec587a015d) | test(snapshot): carry the read guidance into the apply-patch scenarios                                                                                                                                                              |
| [`3841ba6aad`](https://github.com/deepseek-ai/deepseek-harness/commit/3841ba6aad) | test(acp): pin the recorded optional patch tool                                                                                                                                                                                     |
| [`c87fd429e0`](https://github.com/deepseek-ai/deepseek-harness/commit/c87fd429e0) | test(web): align optional tools for recording and replay                                                                                                                                                                            |
| [`3def5c9e24`](https://github.com/deepseek-ai/deepseek-harness/commit/3def5c9e24) | test(fs): pin recorded patch tool and cover configurable defaults                                                                                                                                                                   |
| [`e1609fe164`](https://github.com/deepseek-ai/deepseek-harness/commit/e1609fe164) | fix(fs): temporarily disable apply_patch in shipped defaults                                                                                                                                                                        |
| [`05225052f9`](https://github.com/deepseek-ai/deepseek-harness/commit/05225052f9) | docs(subagent): clarify discovery covers explicit model selection                                                                                                                                                                   |
| [`67e2d1e506`](https://github.com/deepseek-ai/deepseek-harness/commit/67e2d1e506) | fix(python): preserve deployed dependency validation                                                                                                                                                                                |
| [`125fabd8d0`](https://github.com/deepseek-ai/deepseek-harness/commit/125fabd8d0) | fix(python): deploy the runtime from the workspace lockfile                                                                                                                                                                         |
| [`65a87a5860`](https://github.com/deepseek-ai/deepseek-harness/commit/65a87a5860) | fix(subagent): clarify selection guidance and stabilize Web smoke capture                                                                                                                                                           |
| [`c05a34e153`](https://github.com/deepseek-ai/deepseek-harness/commit/c05a34e153) | feat(desktop): reload update settings from configuration errors                                                                                                                                                                     |
| [`f387930ebe`](https://github.com/deepseek-ai/deepseek-harness/commit/f387930ebe) | docs(desktop): align channel guidance with saved settings                                                                                                                                                                           |
| [`5a75c8b4a5`](https://github.com/deepseek-ai/deepseek-harness/commit/5a75c8b4a5) | fix(subagent): require an explicit route for reasoning overrides                                                                                                                                                                    |
| [`7301fa98b0`](https://github.com/deepseek-ai/deepseek-harness/commit/7301fa98b0) | fix(subagent): state when to query child routes and when to omit them                                                                                                                                                               |
| [`8d0c3b4633`](https://github.com/deepseek-ai/deepseek-harness/commit/8d0c3b4633) | fix(settings): show saved API keys and preserve environment detection (#7695)                                                                                                                                                       |
| [`26c077aa5f`](https://github.com/deepseek-ai/deepseek-harness/commit/26c077aa5f) | test(desktop): verify settings recovery in the browser                                                                                                                                                                              |
| [`c0795c003a`](https://github.com/deepseek-ai/deepseek-harness/commit/c0795c003a) | fix(desktop): explain current download filesystem failures                                                                                                                                                                          |
| [`a313ff916b`](https://github.com/deepseek-ai/deepseek-harness/commit/a313ff916b) | docs(cua): proactively recommend developer signing identities                                                                                                                                                                       |
| [`598006d608`](https://github.com/deepseek-ai/deepseek-harness/commit/598006d608) | fix(desktop): log obsolete update cleanup failures without blocking                                                                                                                                                                 |
| [`f578528f1f`](https://github.com/deepseek-ai/deepseek-harness/commit/f578528f1f) | fix(desktop): guide recovery from invalid settings files                                                                                                                                                                            |
| [`d2bd3f3893`](https://github.com/deepseek-ai/deepseek-harness/commit/d2bd3f3893) | feat(desktop): rename stable update channel consistently                                                                                                                                                                            |
| [`1c9a7e6a71`](https://github.com/deepseek-ai/deepseek-harness/commit/1c9a7e6a71) | fix(desktop): preserve updates on repeated channel selection                                                                                                                                                                        |
| [`8e30aa7bdc`](https://github.com/deepseek-ai/deepseek-harness/commit/8e30aa7bdc) | fix(desktop): simplify update channel description                                                                                                                                                                                   |
| [`683daea572`](https://github.com/deepseek-ai/deepseek-harness/commit/683daea572) | test(mcp): await the status reload and refresh the template snapshot after master sync                                                                                                                                              |
| [`2bd4226dc9`](https://github.com/deepseek-ai/deepseek-harness/commit/2bd4226dc9) | fix(cua): preserve system keychain lock lifecycle                                                                                                                                                                                   |
| [`d052805f96`](https://github.com/deepseek-ai/deepseek-harness/commit/d052805f96) | fix(cua): preserve unowned files during developer signing setup                                                                                                                                                                     |
