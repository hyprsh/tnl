## Checks

`scripts/check.sh` runs every check CI runs. Run it before reporting work done, and report its exit status.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on hyprsh/tnl, accessed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
