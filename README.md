# Oodle CLI

Command-line interface for the Oodle observability platform.

`oodle` lets you manage monitors, notifiers, dashboards, synthetic monitors, log
metrics, drop rules, API keys, users, and more from your terminal or CI
pipeline. It is designed to be friendly for both humans (rich tables, prompts)
and agents (deterministic JSON output, exit codes, env-based config).

---

## Installation

### Homebrew (macOS / Linux)

```bash
brew tap oodle-ai/oodle
brew install oodle
```

To upgrade:

```bash
brew upgrade oodle
```

### Download a release binary

Pre-built binaries for macOS, Linux, and Windows are available on the
[Releases](https://github.com/oodle-ai/oodle-cli/releases) page. Download the
archive for your platform, extract it, and place the `oodle` binary on your
`PATH`.

### Install with `go install`

```bash
go install github.com/oodle-ai/oodle-cli/cmd/oodle@latest
```

This places the `oodle` binary in `$(go env GOPATH)/bin`. Make sure that
directory is on your `PATH`.

### Build from source

```bash
git clone https://github.com/oodle-ai/oodle-cli.git
cd oodle-cli
make build
# Binary is written to ./bin/oodle
./bin/oodle version
```

---

## Quick Start

```bash
# Configure (interactive)
oodle configure

# Or use environment variables
export OODLE_API_KEY="your-api-key"
export OODLE_INSTANCE="your-instance"

# List monitors
oodle monitors list

# Get JSON output
oodle monitors list -o json
```

---

## Authentication

`oodle` needs three things to talk to the API:

| Setting    | Purpose                                      | Default                  |
|------------|----------------------------------------------|--------------------------|
| API key    | Authenticates the request                    | _(required)_             |
| Instance   | Identifies your Oodle tenant/instance        | _(required)_             |
| API URL    | The Oodle deployment to talk to              | `https://us1.oodle.ai`   |

Each value can be provided via CLI flag, environment variable, or the config
file. **Resolution order** (highest priority first):

1. CLI flags (`--api-key`, `--instance`, `--api-url`)
2. Environment variables (`OODLE_API_KEY`, `OODLE_INSTANCE`, `OODLE_DEPLOYMENT`
   / `OODLE_API_URL`)
3. Config file at `~/.oodle/config.yaml`
4. Built-in defaults (only for API URL)

The config file is created and managed by `oodle configure`.

---

## Configuration

### `oodle configure`

Save credentials to `~/.oodle/config.yaml`.

**Interactive mode** — run with no flags (or with some flags) in a TTY and
`oodle` will prompt for any missing values:

```bash
oodle configure
```

**Non-interactive mode** — provide all values as flags and nothing is prompted:

```bash
oodle configure \
  --api-key "your-api-key" \
  --instance "your-instance" \
  --api-url "https://us1.oodle.ai"
```

### Config file format

`~/.oodle/config.yaml`:

```yaml
api_key: your-api-key
instance: your-instance
api_url: https://us1.oodle.ai
```

### Environment variables

| Variable           | Equivalent flag | Description                               |
|--------------------|-----------------|-------------------------------------------|
| `OODLE_API_KEY`    | `--api-key`     | API key used to authenticate              |
| `OODLE_INSTANCE`   | `--instance`    | Oodle instance / tenant identifier        |
| `OODLE_DEPLOYMENT` | `--api-url`     | Oodle API URL (e.g. `https://us1.oodle.ai`) |
| `OODLE_API_URL`    | `--api-url`     | Alias for `OODLE_DEPLOYMENT`              |

### Global flags

| Flag                | Description                                                    |
|---------------------|----------------------------------------------------------------|
| `--api-key`         | Oodle API key (overrides `OODLE_API_KEY`)                      |
| `--instance`        | Oodle instance ID (overrides `OODLE_INSTANCE`)                 |
| `--api-url`         | Oodle API URL (overrides `OODLE_DEPLOYMENT` / `OODLE_API_URL`) |
| `-o`, `--output`    | Output format: `table`, `json`, `yaml`, `csv` (auto-detected)  |
| `--force`           | Skip confirmation prompts for destructive actions              |
| `--retries`         | Number of retries for transient API failures (default `3`)     |
| `-h`, `--help`      | Show help for any command                                      |

---

## Commands

The CLI is organized into resource groups. Use `oodle <group> --help` and
`oodle <group> <subcommand> --help` for detailed flags on any command.

### Monitors — `oodle monitors`

Aliases: `monitor`, `mon`.

| Subcommand            | Description                                                |
|-----------------------|------------------------------------------------------------|
| `list`                | List monitors                                              |
| `get <id>`            | Get a monitor by ID                                        |
| `create -f <file>`    | Create a monitor from a JSON/YAML file                     |
| `update -f <file>`    | Update a monitor from a JSON/YAML file                     |
| `delete <id>`         | Delete a monitor (single ID) or many via `--ids`           |
| `state <id>`          | Get a monitor's state                                      |
| `triggers`            | List monitor triggers                                      |
| `expression-insights` | Create an expression insight report for a monitor          |
| `template-files`      | Create monitor template files                              |

```bash
oodle monitors list
oodle monitors get mon-abc123 -o json
oodle monitors create -f monitor.yaml
oodle monitors delete mon-abc123 --force
oodle monitors triggers -o json
```

### Notifiers — `oodle notifiers`

Alias: `notifier`.

| Subcommand          | Description                                |
|---------------------|--------------------------------------------|
| `list`              | List notifiers                             |
| `get <id>`          | Get a notifier by ID                       |
| `create -f <file>`  | Create a notifier from a JSON/YAML file    |
| `update -f <file>`  | Update a notifier from a JSON/YAML file    |
| `delete <id>`       | Delete a notifier                          |

```bash
oodle notifiers list
oodle notifiers create -f slack-notifier.yaml
```

### Notification Policies — `oodle notification-policies`

Aliases: `np`, `notification-policy`.

| Subcommand          | Description                                          |
|---------------------|------------------------------------------------------|
| `list`              | List notification policies                           |
| `get <id>`          | Get a notification policy by ID                      |
| `create -f <file>`  | Create a notification policy from a JSON/YAML file   |
| `update -f <file>`  | Update a notification policy from a JSON/YAML file   |
| `delete <id>`       | Delete a notification policy                         |

```bash
oodle np list -o json
oodle notification-policies get np-123
```

### Muting Rules — `oodle muting-rules`

Aliases: `mr`, `muting-rule`.

| Subcommand          | Description                                |
|---------------------|--------------------------------------------|
| `list`              | List muting rules                          |
| `get <id>`          | Get a muting rule by ID                    |
| `create -f <file>`  | Create a muting rule from a JSON/YAML file |
| `delete <id>`       | Delete a muting rule                       |

```bash
oodle muting-rules list
oodle mr create -f muting-rule.yaml
```

### Log Metrics — `oodle log-metrics`

Aliases: `lm`, `logmetrics`.

| Subcommand          | Description                                          |
|---------------------|------------------------------------------------------|
| `list`              | List log metrics rules                               |
| `get <id>`          | Get a log metrics rule by ID                         |
| `create -f <file>`  | Create a log metrics rule from a JSON or YAML file   |
| `update -f <file>`  | Update a log metrics rule from a JSON or YAML file   |
| `delete <id>`       | Delete a log metrics rule                            |

```bash
oodle log-metrics list
oodle lm create -f log-metric.yaml
```

### Synthetic Monitors — `oodle synthetic-monitors`

Aliases: `sm`, `synthetics`.

| Subcommand          | Description                                              |
|---------------------|----------------------------------------------------------|
| `list`              | List synthetic monitors                                  |
| `get <id>`          | Get a synthetic monitor by ID                            |
| `create -f <file>`  | Create a synthetic monitor from a JSON or YAML file      |
| `update -f <file>`  | Update a synthetic monitor from a JSON or YAML file      |
| `delete <id>`       | Delete a synthetic monitor                               |
| `run <id>`          | Trigger an on-demand run of a synthetic monitor          |

```bash
oodle synthetic-monitors list
oodle sm run sm-123
```

### Dashboards — `oodle dashboards`

Aliases: `dashboard`, `dash`.

| Subcommand          | Description                                              |
|---------------------|----------------------------------------------------------|
| `list`              | List dashboards                                          |
| `get <uid>`         | Get a dashboard by UID                                   |
| `create -f <file>`  | Create or update a dashboard from a JSON or YAML file    |
| `delete <uid>`      | Delete a dashboard                                       |

```bash
oodle dashboards list
oodle dashboards create -f dashboard.json
```

### Folders — `oodle folders`

Alias: `folder`.

| Subcommand   | Description           |
|--------------|-----------------------|
| `list`       | List folders          |
| `create`     | Create a folder       |

```bash
oodle folders list
```

### Drop Rules — `oodle drop-rules`

Aliases: `dr`, `drop-rule`.

| Subcommand          | Description                                    |
|---------------------|------------------------------------------------|
| `list`              | List drop rules                                |
| `get <id>`          | Get a drop rule by ID                          |
| `create -f <file>`  | Create a drop rule from a JSON or YAML file    |
| `update -f <file>`  | Update a drop rule from a JSON or YAML file    |
| `delete <id>`       | Delete a drop rule                             |

```bash
oodle drop-rules list
```

### Metrics — `oodle metrics`

Alias: `metric`. Inspect metrics, labels, and label values.

| Subcommand                          | Description                                |
|-------------------------------------|--------------------------------------------|
| `names`                             | List metric names                          |
| `labels <metric>`                   | List label names for a metric              |
| `label-values <metric> <label>`     | List values for a label of a metric        |

```bash
oodle metrics names -o json
oodle metrics labels http_requests_total
oodle metrics label-values http_requests_total status
```

### Traces — `oodle traces`

Alias: `trace`. Query traces, trace labels, and label values.

| Subcommand            | Description                            |
|-----------------------|----------------------------------------|
| `list`                | List traces in a time range            |
| `get <id>`            | Get a trace by ID                      |
| `labels`              | List trace label names                 |
| `label-values <label>`| List values for a trace label          |

```bash
oodle traces labels -o json
oodle traces list
```

### API Keys — `oodle api-keys`

Aliases: `ak`, `api-key`.

| Subcommand          | Description           |
|---------------------|-----------------------|
| `list`              | List API keys         |
| `get <id>`          | Get an API key by ID  |
| `create`            | Create an API key     |
| `delete <id>`       | Delete an API key     |

```bash
oodle api-keys list
```

### Users — `oodle users`

| Subcommand        | Description                              |
|-------------------|------------------------------------------|
| `list`            | List users in the organization           |
| `invitations`     | Manage user invitations (sub-group)      |

```bash
oodle users list -o json
oodle users invitations --help
```

### Other commands

| Command       | Description                                    |
|---------------|------------------------------------------------|
| `configure`   | Configure the Oodle CLI                        |
| `version`     | Print the oodle CLI version                    |
| `completion`  | Generate shell autocompletion scripts          |
| `help`        | Help about any command                         |

---

## Output Formats

Use `-o` / `--output` to control how results are rendered.

| Format  | Use case                                     |
|---------|----------------------------------------------|
| `table` | Human-readable table (default when stdout is a TTY) |
| `json`  | Machine-readable JSON (default when stdout is _not_ a TTY) |
| `yaml`  | Machine-readable YAML                        |
| `csv`   | CSV with a header row, suitable for spreadsheets |

### Auto-detection

If you do not pass `--output`, `oodle` picks a sensible default:

- Stdout is a terminal → `table`
- Stdout is a pipe or file → `json`

This means commands like `oodle monitors list | jq` Just Work without needing
to pass `-o json`.

### Examples

```bash
# Pretty table for humans
oodle monitors list

# JSON for jq / scripting
oodle monitors list -o json | jq '.[] | .id'

# YAML
oodle monitors list -o yaml

# CSV (first line is headers)
oodle monitors list -o csv > monitors.csv
```

---

## Agent / CI Usage

`oodle` is designed to work well in non-interactive environments such as CI
pipelines and AI agents.

- **Configure with environment variables** so you don't need a config file:

  ```bash
  export OODLE_API_KEY="$OODLE_API_KEY"
  export OODLE_INSTANCE="prod"
  export OODLE_DEPLOYMENT="https://us1.oodle.ai"
  ```

- **Force JSON output** for predictable parsing — either pass `-o json` or
  rely on auto-detection (stdout is not a TTY in CI, so JSON is the default).

  ```bash
  oodle monitors list -o json | jq '.[].id'
  ```

- **Exit codes**:
  - `0` — success
  - non-zero — error (authentication failure, not-found, validation error,
    network error, etc.)

  Errors are written to stderr; structured output (when applicable) goes to
  stdout.

- **Skip confirmation prompts** for destructive operations with `--force`:

  ```bash
  oodle monitors delete mon-abc123 --force
  ```

- **Pipe-friendly**: when stdout is not a TTY, the default output format
  becomes JSON automatically. No interactive prompts are issued in
  non-interactive mode (the CLI errors out clearly instead).

---

## File Input

Create / update commands accept a JSON or YAML file via `-f` / `--file`. The
format is detected from the file extension (`.json`, `.yaml`, `.yml`).

```bash
# YAML
oodle monitors create -f monitor.yaml

# JSON
oodle dashboards create -f dashboard.json

# Update from a file
oodle log-metrics update -f log-metric.yaml
```

The same file format is used for all resource types that support `create` and
`update`. Use `oodle monitors template-files` to generate starter templates
for monitors.

---

## Development

This project uses Go and a simple Makefile. Common commands:

```bash
# Build the binary into ./bin/oodle
make build

# Run unit tests
make test

# Run integration tests (requires OODLE_API_KEY and OODLE_INSTANCE)
make test-integration

# Regenerate API client code (if specs change)
make generate

# Run linters
make lint

# Remove build artifacts
make clean
```

### Layout

```
cmd/oodle/        # main entry point
internal/         # CLI commands, output formatting, client wiring
api/              # OpenAPI specs
test/             # integration tests (build tag: integration)
```

### Releasing

Releases are automated via [GoReleaser](https://goreleaser.com/) and GitHub
Actions. To cut a new release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

This triggers the release workflow which:

1. Builds cross-platform binaries (macOS/Linux/Windows, amd64/arm64)
2. Creates a GitHub Release with the binaries and checksums
3. Updates the Homebrew formula in
   [oodle-ai/homebrew-oodle](https://github.com/oodle-ai/homebrew-oodle)

**Prerequisites** (one-time setup):

1. Create the [oodle-ai/homebrew-oodle](https://github.com/oodle-ai/homebrew-oodle)
   repository with an empty `Formula/` directory.
2. Create a GitHub Personal Access Token for GoReleaser to push the formula
   update. Prefer a **fine-grained** PAT scoped only to the
   `oodle-ai/homebrew-oodle` repository with `Contents: Read and write`
   permission. A classic PAT with `repo` scope also works but grants broader
   access than necessary.
3. Add the token as a repository secret named `HOMEBREW_TAP_GITHUB_TOKEN` in
   the oodle-cli repo settings.

### Running integration tests

Integration tests live under `test/` and are gated by the `integration` build
tag. They will skip automatically if `OODLE_API_KEY` or `OODLE_INSTANCE` are
not set:

```bash
OODLE_API_KEY=... OODLE_INSTANCE=... \
  go test -tags integration -v -count=1 ./test/...
```

### License

See [LICENSE](./LICENSE).
