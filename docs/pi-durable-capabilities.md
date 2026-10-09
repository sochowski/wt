# Pi Durable capability audit (#75)

## Scope and evidence

Candidate audit of WT base `17667fb6cb476b7edd692f1197fdc60ec2b3a69e`,
`wt-durable-v1`: pi-durable/pi-ai/chord 1.0.3 and pi-tui 0.87.1 (locked).
Native comparison: locally installed `@earendil-works/pi-coding-agent` 0.87.1
`docs/{configuration,skills,settings,sdk}.md`; Durable public package README
(system-prompt sections, registry, CodingTools) and WT source below.
This is a source/documentation audit, not a live-provider or installed-profile
behavior proof, independent acceptance, or approval to deploy. User settings,
credentials, live stores and installed runtime files were not changed.

**Native-style UI is not native engine, extension or workflow parity.**
Harness/SQLite remains the sole Durable inference/state owner. Do not copy a
native prompt to advertise tools that are not actually registered.

Labels: **equivalent** means the stated bounded behavior matches; **intentional
difference** is a current safety/ownership boundary; **pending** is separately
tracked work, not available now; **unsupported** has no implementation in this
profile. Labels do not imply all native behavior in an area is equivalent.

## Capability matrix

| Area | Classification | Current Durable behavior / native comparison |
|---|---|---|
| Prompt assembly | Intentional difference | WT registers `wt-instructions`, delegation-results and orientation sections through the public Harness registry. Native prompt replacement/append files (`SYSTEM.md`, `APPEND_SYSTEM.md`) are not loaded. No native SDK session engine. |
| Project instructions | Equivalent (bounded) | Agent-directory instructions first, then filesystem ancestors root-to-cwd. One file per directory: `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`, `CLAUDE.md`, `CLAUDE.MD`, in that order. Canonical-path deduplication; override only replaces the same directory's ordinary instructions. These nonexecuting instructions do not require project trust. |
| User skills | Equivalent (bounded) | Recursively discover `SKILL.md` directories in agent-dir `skills/`, then `~/.agents/skills/`. First duplicate name wins with a diagnostic; instructions loaded on demand. |
| Project skills | Intentional difference | Require explicit per-launch `WT_DURABLE_TRUST_PROJECT=1`; only cwd `.pi/skills/` then `.agents/skills/`. Native docs also describe ancestor `.agents/skills/` discovery up to repository root. Trust does not enable project settings or executable extensions. |
| Skill invocation | Equivalent (bounded) | `disable-model-invocation: true` excludes the skill from the advertised prompt but retains explicit `/skill:NAME [TEXT]` invocation. Paths in skill instructions are relative to that skill directory. |
| Skill syntax/resources | Unsupported | Only bounded scalar frontmatter and directories containing `SKILL.md`; no full YAML, standalone Markdown skills, settings-specified resource paths, package skill loading or resource `/reload`. Native `enableSkillCommands` is not consumed; discovered skills remain in command completion. |
| Default model | Intentional difference | Fresh bootstrap requires explicit user `defaultProvider/defaultModel` in the pinned catalog; no fallback. `models.json`, custom providers and saved model profiles are not loaded. Resume retains the stored conversation model. |
| Thinking defaults (#69) | Equivalent (bounded) | Fresh bootstrap uses `modelThinkingLevels[provider/model]`, then `defaultThinkingLevel`, then `medium`, clamped by public model capabilities with a diagnostic. Resume does not reread defaults. `/thinking` persists only in the conversation. #69 is already fixed by #70; no duplicate fix needed. |
| Presentation settings | Equivalent (bounded) | Read-only user theme/custom theme, `editorPaddingX`, `outputPad`, `autocompleteMaxVisible`, `hideThinkingBlock`, `showHardwareCursor`, and native keybinding action IDs. No settings writes. Fullscreen requests have a visible scrollback fallback diagnostic. |
| Other native settings | Unsupported | Project `.pi/settings.json`, package/resource declarations, native compaction/retry preferences and other unlisted settings are not applied. Harness compaction is disabled and retry count is zero. Unsupported settings are not all diagnosed individually: absence of an error is not support. |
| Authentication | Intentional difference | Read-only saved API keys and sufficiently fresh OAuth. No key-command execution, refresh/login/logout or credential writes; unsupported/expired credentials fail visibly. |
| Coding tools | Equivalent (bounded) | Public Durable `read`, `write`, `edit`, `bash`, guarded by WT fencing and replay-unsafe marking. Native read-image support is not supplied by the pinned Durable CodingTools. Read-only children get only `read`; assigned-job paths and host execution are additionally bounded. Bash is not an OS sandbox. |
| Managed WT tools | Intentional difference | Explicit public host bridges: `wt_workspace`, `wt_view`, `wt_agent`, `wt_present_deck`, `wt_delegate`. Managed tools are omitted for bootstrap/read-only/job contexts; workspace/tool ceilings still apply. Tool schema and host admission, not prompt prose, define availability. |
| Delegation | Intentional difference | `wt_delegate` is built-in Durable writer plus mandatory independent read-only reviewer with host-observed evidence. Named peers have independent lifetimes. Neither is native subagent/council plugin parity. |
| Questionnaire/todo | Pending (#71) | Unaccepted frozen M2 source is not shipped in this v1 profile. Native-looking UI is not evidence that these calls exist. |
| Web / MCP / scripting | Pending (#72/#73) | No actual web plugin or MCP adapter registered here. Do not infer support from a native extension/package or WT's other backend configurations. |
| Custom subagents / councils | Pending (#74) | Genuine plugin orchestration with independent child stores and authoritative budgets is separate work. `wt_delegate` alone does not satisfy it. |
| Native extension lifecycle | Intentional difference | No automatic native extension/package script execution, native custom-tool registration, or competing SDK inference owner. |
| Native-only UI commands/actions | Unsupported | Unknown slash commands fail visibly without becoming model input. External editor, image paste, suspend, dequeue, clipboard message copy, saved-model actions and unrecognized bound actions report unsupported. Supported commands are listed by `/help`; `/quit` closes recoverably, `/abort` aborts, `/stop` is a separate WT human stop. |

## Source and private regression coverage

- `runtime/pi-durable/resources.mjs`: context order, discovery, trust, scalar
  syntax, model-disabled advertisement and read-only credentials.
- `runtime/pi-durable/runtime.mjs`: registered tools/sections, fences,
  bootstrap-only settings, model/thinking defaults and Harness configuration.
- `runtime/pi-durable/{presentation,tui}.mjs`: supported settings/actions,
  skill commands, explicit unsupported commands and lifecycle behavior.
- `runtime/pi-durable/{wt-tools,jobs}.mjs`: host transport and bounded roles.
- `resources.test.mjs`: precedence/trust, no project settings execution,
  disabled advertisement, duplicate names, unsupported frontmatter,
  no project-ancestor discovery and prompt override exclusion.
- `runtime.test.mjs`: thinking precedence/clamp/resume (#69), private faux-model
  TUI invocation and unsupported-command rejection.
- `presentation.test.mjs`: private theme/settings/keybinding behavior and
  invalid-setting rejection.

Run `node runtime/pi-durable/run-tests.mjs` for isolated HOME/state and synthetic
models. Do not run tests against real Pi credentials, stores or providers.
The existing broad WT suite and private compiled WT/PTY gates remain separate
requirements before publication; a focused audit test pass is not suite-green.

## Candidate validation results

- Locked dependency install: `npm ci --ignore-scripts --no-audit --no-fund`;
  no lifecycle scripts or dependency edits.
- Isolated runtime suite: **53/53 passed**, including four added audit tests.
  `/help` now states the v1 owner/resource/plugin boundaries and links this
  matrix; the private TUI test verifies it makes no inference request and
  preserves the conversation's model/thinking.
- Production dependency audit: **0 vulnerabilities**.
- `git diff --check`: passed.
- Broad `./test.sh`: **192 passed, 8 failed** (Go tests, staging install,
  shell metadata/cwd, shell wait echo matching, file scene, deck-next,
  presentation socket and `/var` versus `/private/var` main-window cwd).
  Go output includes canonical-path admission refusals. These failures were
  not repaired here, and no clean-base comparison was performed; do not claim
  broad-suite green or independently verified baseline attribution.

Full local outputs are in the session scratch directory:
`parity-runtime.tap`, `parity-audit.json`, `parity-broad.txt`.
Managed diff creation initially returned a host-command error, but subsequent
host inspection confirmed the owned `wt-parity` diff is available. It was reused
without moving focus; no raw tmux or protection bypass was used.

## Remaining acceptance

Verify actual installed-profile behavior only under approved private fixtures;
this audit does not probe the owner's running conversations. Independent native
read-only review remains required and the reviewer-replacement decision from
#79 is unresolved. No frozen M2 sources were reconciled or changed. Update this
matrix after each accepted milestone; obtain separate merge/install approval.
