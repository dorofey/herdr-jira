# Changelog

## Unreleased

### Changed

- Make selected rows stand out with a full-width accent background, bold
  contrasting text, and a `›` marker. Inline status colors no longer reduce
  readability on the selected row.
- Color workflow status headings and issue identifiers: blocked and rejected
  statuses red, on hold and waiting amber, to-do yellow, review purple, testing
  teal, backlog lavender, triage orange, in progress blue, and completed green.
  Jira workflow categories continue to control issue behavior.

## 0.1.0 — 2026-10-14

### Added

- Initial Jira Cloud plugin adapted from
  [Magnús Ólafsson's herdr-linear](https://github.com/mrolafsson/herdr-linear).
- API token sign-in, multiple Jira sites, issue and project browsing, and
  formatted issue descriptions.
- Workflow transitions, issue creation, attachments, worktrees, agent handoff,
  sidebar labels, and an offline demo.
- README acknowledgements linking to the original author and project.
