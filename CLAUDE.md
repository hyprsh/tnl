## Checks

`scripts/check.sh` runs every check CI runs. Run it before reporting work done, and report its exit status.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on hyprsh/tnl, accessed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Commits

Commit messages are Conventional Commits. release-please builds `CHANGELOG.md`, the next version and the release notes from them, so the type decides whether a commit is listed (`feat`, `fix`, `perf`, `revert`) and how the version moves.

Before 1.0, mark a change to a wire format or to `state.json`'s shape as breaking (`feat!:`, or a `BREAKING CHANGE:` footer that says what to upgrade), so it ships in a new 0.x minor (spec §12).
