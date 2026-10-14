# Validation

- macOS ARM64 build: passed (`sh scripts/build.sh`).
- CLI version and help: passed (`herdr-jira 0.1.0`).
- Eight API and rendering tests: passed (`go test ./...`), using an in-memory HTTP transport.
- Terminal demo: opened the grouped issue list, opened an issue detail with rendered Markdown, and returned to the list and closed it.
- Linux cross-build: blocked by an uncached `github.com/godbus/dbus/v5` dependency and sandbox restrictions on the module cache. Linux runtime is unverified.
- Live Jira login, API mutations, Keychain persistence, and actual Herdr popup/worktree/agent handoff: unverified; no account credentials were provided and no live Jira mutations were performed.
- CodeGraph initialization: attempted as requested, but `codegraph` is not installed (`command not found`). No graph was created.
