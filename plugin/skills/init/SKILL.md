---
name: init
description: Set Relay up in the current repository, once per project. Three questions, then relay.yml, the workflow, labels and a CLAUDE.md section, committed and pushed. Use when the owner runs /relay:init.
---

You are setting Relay up in the repository in the current directory. This
runs interactively, in the owner's own Claude Code session, not in CI.

## 0. Preconditions

Run these and stop with a clear message if any fails:

- `gh auth status` — the owner is logged in.
- `gh repo view --json nameWithOwner,visibility,defaultBranchRef` — this is a
  GitHub repository. **If `visibility` is `PUBLIC`, refuse**: Relay runs a
  self-hosted runner on the owner's Mac and reacts to Issue comments; in a
  public repository anyone could post one. Say that and stop.
- `gh api user -q .login` — the owner's login. It goes into `relay.yml` as
  `owner`; only this login can drive the pipeline.
- `git status --porcelain` — warn if the tree is dirty, but continue.
- If `.github/workflows/relay.yml` or `relay.yml` already exists, say so and
  ask whether to overwrite before touching either.

## 1. Three questions

Ask them one at a time, with AskUserQuestion when it is available:

1. **Platform**: `android` (preset `android-release`), `web` (preset
   `web-vercel`, v1), `desktop` (preset `desktop-tauri`, v2). Only
   `android-release` ships in v0; for the others, write the preset name and
   say the stages beyond plan arrive with that preset.
2. **Gates**: `[plan]` (recommended for v0: the pipeline stops before the
   plan is approved) or `[design, plan, deploy]`.
3. **Model for thinking stages** (triage, plan, review): `opus` (recommended)
   or `fable`. If `fable`: warn that in headless runs it can bill usage
   credits beyond the subscription without asking, and that the stage
   comment will say when it was used.

## 2. Files

Templates are in `${CLAUDE_PLUGIN_ROOT}/templates/`. Render them, do not
rewrite them from memory:

- `relay.yml` from `templates/relay.yml`: fill `owner`, `platform`, `preset`,
  `gates`, and the thinking model in `models`. Keep every comment.
- `.github/workflows/relay.yml` from `templates/workflow.yml`, verbatim.
- `CLAUDE.md`: append `templates/claude-md-section.md` unless the file already
  has a `## Relay` heading. Create `CLAUDE.md` if it does not exist.
- `.gitignore`: add a line `.relay/` if it is not there.
- `docs/specs/.gitkeep` so that the directory exists.

## 3. Labels

Create each with `gh label create <name> --color <hex> --description "<text>" --force`:

| Label | Color | Description |
|---|---|---|
| `relay` | `0E8A16` | У пайплайні Relay |
| `needs-approval` | `FBCA04` | Чекає рішення людини |
| `relay:discuss` | `C5DEF5` | Чернетка: іде обговорення |
| `relay:triage` | `BFD4F2` | Етап: тріаж |
| `relay:plan` | `BFD4F2` | Етап: план |
| `relay:implement` | `BFD4F2` | Етап: код (v1) |
| `relay:test` | `BFD4F2` | Етап: тести (v1) |
| `relay:review` | `BFD4F2` | Етап: огляд (v1) |
| `relay:deploy` | `BFD4F2` | Етап: деплой (v1) |
| `relay:done` | `0E8A16` | Задеплоєно |
| `relay:failed` | `B60205` | Зупинено після помилки або /abort |
| `relay:queued` | `EDEDED` | У черзі за іншою задачею |

## 4. Commit and push

Show what changed (`git status`, the new files) and ask for confirmation.
Then:

```
git add relay.yml .github/workflows/relay.yml CLAUDE.md .gitignore docs/specs/.gitkeep
git commit -m "Add Relay pipeline"
git push origin <default branch>
```

Explain in one line why the push matters: a workflow triggered by Issue
comments runs only from the default branch, so until this lands there, Relay
cannot react to anything.

## 5. What is next

Finish with the two remaining steps from the setup, verbatim:

```
relay add <alias> <owner/repo>   # registers the runner for this repo and the bot alias
```

and, to try it: send `<alias>: тестова задача` to the bot.
