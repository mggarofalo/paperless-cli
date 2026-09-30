# paperless-cli

A Go CLI for [Paperless-ngx](https://github.com/paperless-ngx/paperless-ngx), designed for people and AI agents organizing documents. Inspired by [plane-cli](https://github.com/mggarofalo/plane-cli): JSON by default, OS-keyring credentials, predictable exit codes, and composable commands.

## Build and install

Requires Go 1.25 or newer.

```sh
git clone https://github.com/mggarofalo/paperless-cli.git
cd paperless-cli
go build -o paperless .
```

On Windows, build and install into your user PATH:

```powershell
.\install.ps1
paperless --help
```

The Windows installer builds from this checkout, installs to `~/.local/bin`, and adds that directory to your user PATH if needed. It also updates the calling PowerShell session's PATH. Restart other existing terminals/apps if the directory was newly added. Re-run the installer to upgrade; existing PATH entries are preserved and no duplicate entry is added. Use `-InstallDir PATH` to choose a different directory. Administrator privileges are not required for the default location.

`make install` runs the same installer on Windows. On Linux/macOS it installs the `paperless` binary to your Go bin directory; ensure that directory is on PATH by adding `export PATH="$(go env GOPATH)/bin:$PATH"` to your shell startup file and opening a new terminal. Alternatively, `go install github.com/mggarofalo/paperless-cli@latest` installs a binary named `paperless-cli` (the module directory name); its commands are identical.

CI tests Linux, macOS, and Windows. Pushing a `v*` tag runs the release workflow to publish binaries and checksums for all three platforms on amd64 and arm64. The release job runs on macOS so its binaries retain native Keychain support (CGO); Linux and Windows builds do not require CGO.

## Authentication

Create an API token in Paperless's **My Profile** screen, then run:

```sh
paperless auth login
```

The CLI prompts for your instance URL and API token (hidden while typing). A hostname without a scheme defaults to HTTPS. It verifies API access, saves the URL in your local profile, and stores the token in the OS keyring. Future commands use these saved credentials; no environment variables are required.

```sh
paperless auth status --check
paperless document list
```

Run login again to replace the token. The saved URL appears as a default; press Enter to keep it or type a different URL. You can also supply the URL explicitly with `paperless auth login --url https://paperless.example.com`. Use `paperless auth logout` to remove the saved credentials.

For agent or CI use, environment variables are an optional alternative:

```sh
export PAPERLESS_URL=https://paperless.example.com
export PAPERLESS_API_TOKEN=your-token
paperless auth status --check
```

PowerShell:

```powershell
$env:PAPERLESS_URL = 'https://paperless.example.com'
$env:PAPERLESS_API_TOKEN = 'your-token'
paperless auth status --check
```

Login uses `PAPERLESS_API_TOKEN` if present; otherwise it prompts without echo, even when a token was previously saved. For noninteractive login, `--url URL --token-stdin` reads a token from stdin. There is no plaintext token fallback. Headless environments without a keyring should use the environment variable.

`--profile staging` (or `PAPERLESS_PROFILE=staging`) selects separate saved credentials. URL precedence is `--url` > `PAPERLESS_URL` > the saved profile. Token precedence is `PAPERLESS_API_TOKEN` > the OS keyring. Saved credentials are bound to both profile and URL. Config contains only profile URLs, under the OS user config directory at `paperless-cli/config.json`; `XDG_CONFIG_HOME` overrides that directory. Logout removes saved credentials, not environment variables.

## Find and inspect documents

```sh
paperless document list --page-size 25
paperless document search 'invoice AND electricity' --all
paperless document list --filter correspondent__id=7 --ordering=-created
paperless document list --filter is_tagged=false --all
paperless document get 42
paperless document content 42
paperless document metadata 42
paperless tag list --all --output table
paperless correspondent list --all
```

Search forwards Paperless's full-text query syntax. `--filter key=value` can be repeated for API filters, including custom-field queries. `content` returns a JSON object with the document ID, title, and OCR text. `metadata` returns the file metadata endpoint response.

Default list output is the server's response, including pagination links. `--all` follows every page and returns `{"count": N, "results": [...]}`. `--page` selects the starting page. Pagination is confined to the configured API, and cycles fail explicitly.

## Organize documents

```sh
paperless tag create --name Reviewed
paperless correspondent create --name 'Utility Company'
paperless document-type create --name Invoice

paperless document update 42 --title 'Electricity bill - June' --dry-run
paperless document update 42 --correspondent 'Utility Company' --document-type Invoice
paperless document update 42 --tags 'Reviewed,Utilities'
paperless document update 42 --correspondent null
paperless document update 42 --tags=
```

Updates use PATCH and send only supplied fields. Relationship flags accept positive numeric IDs or exact, case-insensitive names. Missing or ambiguous names fail before mutation. Numeric IDs avoid lookup requests. `null` clears correspondent, document type, or storage path. `--tags` **replaces the complete tag list**; `--tags=` clears it. Use bulk `add_tag`, `remove_tag`, or `modify_tags` to preserve unrelated tags.

Every writable resource supports `--json` as an inline object, `@filename`, or `-` for stdin. Use API field names. Explicit flags override matching JSON fields. Relationships inside JSON use numeric IDs. File input avoids shell quoting differences:

```json
{
  "custom_fields": [{"field": 1, "value": "2026-06-30"}],
  "archive_serial_number": 123
}
```

Save that as `patch.json`, then:

```sh
paperless document update 42 --json @patch.json --dry-run
paperless document update 42 --json @patch.json
```

`--dry-run` prints the method, API-relative path, and body without sending the mutation. Resolving names may still perform reads. It previews the request; it does not validate permissions or API-specific field rules. Read commands still perform reads. Authentication and downloads reject `--dry-run`.

## Bulk organization

Bulk operations require an explicit set of document IDs. For example, save this as `tags.json`:

```json
{"add_tags": [3], "remove_tags": [1]}
```

```sh
paperless document bulk --documents 42,43 --method modify_tags --json @tags.json --dry-run
paperless document bulk --documents 42,43 --method modify_tags --json @tags.json
```

Supported methods: `set_correspondent`, `set_document_type`, `set_storage_path`, `add_tag`, `remove_tag`, `modify_tags`, `modify_custom_fields`, `set_permissions`, `reprocess`, and `delete`. See `document bulk --help` and the [official bulk API documentation](https://docs.paperless-ngx.com/api/#bulk-editing) for each method's parameters. Bulk parameters use numeric IDs.

Bulk acceptance is not a guarantee that background processing has completed. Read affected documents afterward to verify the intended result. The CLI does not automatically retry mutations.

Deletion requires `--yes` unless previewing:

```sh
paperless document delete 42 --dry-run
paperless document delete 42 --yes
```

## Upload and download

```sh
paperless document upload invoice.pdf --field title='June invoice' --field tags=3
paperless task list --filter task_id=UUID_FROM_UPLOAD
paperless document download 42 --file invoice.pdf
paperless document download 42 --original --file original.pdf
```

Uploads use multipart form data and return the server's task UUID, not a document ID. Poll `task list` to inspect consumption status. Repeat `--field tags=ID` for multiple tags. Metadata fields accept API values and numeric relationship IDs. Uploads spool multipart data to a temporary file with bounded memory; the temporary file is removed when the request ends.

Downloads require a new destination file, never overwrite existing files, and remove partial output if downloading fails. Their stdout is a JSON receipt; bytes go to the requested file.

## Resources and API discovery

| Resource | Commands |
| --- | --- |
| `document` | list, search, get, content, metadata, update, delete, bulk, upload, download |
| `tag`, `correspondent`, `document-type`, `storage-path` | list, get, create, update, delete |
| `custom-field`, `saved-view`, `workflow` | list, get, create, update, delete |
| `task` | list (filter by `task_id` for a consumption task) |

Use `--json` for resource-specific fields such as storage-path templates, matching rules, custom-field data types, and workflow triggers. Available fields depend on your Paperless version.

```sh
paperless schema
paperless api GET documents/42/notes/
paperless api PATCH documents/42/ --json @patch.json --dry-run
```

`schema` retrieves your instance's OpenAPI schema. `api METHOD PATH` provides access to additional endpoints; paths are relative to `/api/`. It supports JSON object bodies and repeated query `--filter` flags. It is a low-level escape hatch: POST endpoints can perform destructive operations, so inspect the schema before using them. API docs: [Paperless REST API](https://docs.paperless-ngx.com/api/).

## Agent and scripting contract

- JSON is the default. `--output table` is available for interactive inspection.
- stdout contains data; stderr contains diagnostics. Successful DELETE commands emit no stdout.
- No interactive prompts outside `auth login`. Destructive delete commands require `--yes`.
- `--timeout` sets the per-request timeout (default `60s`). Interrupts cancel requests.
- Requests stay within the configured API namespace. Redirects are rejected; use the instance's canonical URL, including any subpath.
- No instance URLs or credentials are bundled in this repository.

| Exit code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Network, configuration, or other error |
| 2 | Authentication/permission error (401, 403) |
| 3 | Not found (404) |
| 4 | Validation error (local validation, 400, 422) |
| 5 | Rate limited (429) |

See [the agent workflow guide](docs/agents.md) for a practical organization loop. Agents can invoke the binary directly through their shell tools.

## Development

```sh
go test ./...
go vet ./...
go build -o bin/paperless .
```

Tests use local HTTP test servers and temporary files. They cover authentication headers, pagination, request boundaries, patch semantics, name resolution, dry runs, file transfers, JSON input, and exit codes. Live-instance compatibility must be verified separately; no live instance is needed to build or test.
