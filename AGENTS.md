# Repository conventions

- This repository implements a Go CLI for Paperless-ngx. Keep all examples generic; do not commit real instance URLs, document contents, or credentials.
- Match the existing CLI contract: JSON stdout, diagnostics stderr, explicit errors, no prompts except interactive authentication.
- Use PATCH for updates and preserve omitted fields. Keep dry-run behavior free of mutation requests.
- Use `gofmt`, `go test ./...`, and `go vet ./...` before committing changes.
- Tests use local HTTP servers, never a real Paperless instance by default.
- API transport belongs in `internal/api`; command behavior and configuration belong in `internal/cli`.
- On Windows, use `exec_command` with `tty: true`, including read-only commands. Poll running commands with `write_stdin`. Start background helpers with `Start-Process -WindowStyle Hidden`.
- Changes to protected `main` go through a PR and passing CI. For releases, follow `.agents/skills/release/SKILL.md`; a tag push triggers publication.
