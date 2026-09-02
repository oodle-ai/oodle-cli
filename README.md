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

# Or login with OAuth (interactive browser flow)
oodle auth login

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
| Auth       | API key or OAuth access token                | _(required)_             |
| Instance   | Identifies your Oodle tenant/instance        | _(required)_             |
| API URL    | The Oodle deployment to talk to              | `https://us1.oodle.ai`   |

Each value can be provided via CLI flag, environment variable, or the config
file. **Resolution order** (highest priority first):

1. CLI flags (`--api-key`, `--instance`, `--api-url`)
2. Environment variables (`OODLE_API_KEY` / `OODLE_OAUTH_ACCESS_TOKEN`,
   `OODLE_INSTANCE`, `OODLE_DEPLOYMENT` / `OODLE_API_URL`)
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

### `oodle auth login`

Run browser-based OAuth login and save tokens to `~/.oodle/config.yaml`.
If an API key already exists in config, `oodle` asks whether to delete it.
If both API key and OAuth token remain configured, OAuth is used.
OAuth access tokens are refreshed automatically using the saved refresh token.

```bash
oodle auth login
```

Optional flags:

```bash
oodle auth login -d us1
```

The `-d`/`--deployment` flag accepts a deployment slug (`us1`, `ap1`, `eu1`)
or a full deployment URL/host.

### `oodle auth logout`

Clear saved OAuth credentials from `~/.oodle/config.yaml`.

```bash
oodle auth logout
```

### `oodle auth status`

Show current auth configuration and precedence.

```bash
oodle auth status
oodle auth status -o json
```

### `oodle auth token`

Print the current OAuth access token (environment override first, then saved config).
If the saved token has expired, the command returns an error and does not print it.

```bash
oodle auth token
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
| `OODLE_OAUTH_ACCESS_TOKEN` | _(none)_ | OAuth bearer access token                 |
| `OODLE_OAUTH_REFRESH_TOKEN` | _(none)_ | OAuth refresh token (advanced/manual use) |
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

A webhook or Rootly notifier can carry a custom payload, a JSON object whose
every string is a Go template rendered against the alert data:

```yaml
name: rootly-notifier
type: 7
rootly_config:
  url: https://webhooks.rootly.com/webhooks/incoming/alertmanager_webhooks
  send_resolved: true
  max_alerts: 0
  http_config:
    authorization:
      type: Bearer
      credentials: rootly_alert_source_bearer_token
  payload:
    summary: "{{ .CommonLabels.alertname }} is {{ .Status }}"
    severity: "{{ .CommonLabels._oodle_severity }}"
```

Oodle adds the standard Alertmanager fields to a Rootly payload as it delivers
the alert, so write only your own keys. A webhook notifier (`type: 4`) instead
replaces the whole body with what the payload holds.

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

### GenAI — `oodle genai`

Aliases: `llmops`, `ai`. The evaluation side of Agent
Observability: versioned prompts, evaluation datasets,
evaluators, scores, and experiment runs. Reading GenAI
telemetry stays under `oodle traces` and `oodle metrics`.

| Subcommand      | Description                                            |
|-----------------|--------------------------------------------------------|
| `prompts`       | Versioned prompts, resolved by label                   |
| `datasets`      | Evaluation datasets and their items                    |
| `templates`     | LLM-as-judge and code judges (Evaluations > Library)   |
| `evaluators`    | Run templates over live traffic (Evaluations > Evaluators) |
| `scores`        | Evaluator output and manual scores                     |
| `experiments`   | Run a prompt over a dataset and score it               |
| `connections`   | Provider credentials evaluators and experiments use    |

#### Prompts — `oodle genai prompts`

Every create adds a **version**; applications resolve a prompt
by **label** (`production` by default), so moving a label is
how a new version is rolled out with no deploy.

| Subcommand         | Description                                    |
|--------------------|------------------------------------------------|
| `list`             | List prompts (one row per name)                |
| `get <name>`       | Get a prompt by name, version, or label        |
| `versions <name>`  | List a prompt's versions                       |
| `create -f <file>` | Create a prompt version                        |
| `label <name>`     | Add or replace labels on a version             |
| `delete <name>`    | Delete a prompt, or one version with `--version` |

```bash
oodle genai prompts get support-reply
oodle genai prompts label support-reply --version 4 --labels production
```

#### Datasets — `oodle genai datasets`

Aliases: `dataset`, `ds`. Datasets are versioned by time:
`items --at <RFC3339>` recovers exactly the inputs a past
experiment ran against.

| Subcommand              | Description                        |
|-------------------------|------------------------------------|
| `list`                  | List datasets                      |
| `get <name>`            | Get a dataset by name              |
| `create`                | Create a dataset                   |
| `delete <name>`         | Delete a dataset and its items     |
| `items list <name>`     | List a dataset's items             |
| `items get <id>`        | Get a dataset item                 |
| `items create -f <file>`| Add an item to a dataset           |
| `items update <id>`     | Update a dataset item              |
| `items delete <id>`     | Delete a dataset item              |
| `schedule get <name>`   | Get the dataset's recurring run    |
| `schedule set <name>`   | Set the dataset's recurring run    |
| `schedule delete <name>`| Delete the dataset's recurring run |

```bash
oodle genai datasets create --name support-eval
oodle genai datasets items list support-eval --at 2026-08-01T00:00:00Z
```

##### Schedules — `oodle genai datasets schedule`

A dataset carries at most one schedule, which runs its
experiment without anyone starting it. `set` replaces the whole
definition, and takes the same config flags as
`experiments run`. Two shapes:

- **calendar** — `--time HH:MM` (repeatable) in `--timezone`,
  optionally narrowed by `--weekday` or `--day-of-month`. The
  times follow daylight saving rather than drifting twice a
  year.
- **interval** — `--every 30m|6h|1d`, at least 5m and at most
  365d. No timezone applies to a duration.

A firing starts shortly after it is due rather than exactly on
the minute, and a schedule that falls behind runs once instead
of replaying every firing it missed.

```bash
# Every six hours.
oodle genai datasets schedule set support-eval --every 6h \
  --dataset-id "$DS" --connection-id "$CONN" \
  --prompt-name support-reply --model gpt-4o

# Weekday mornings, Los Angeles time.
oodle genai datasets schedule set support-eval \
  --time 09:00 --weekday monday --weekday friday \
  --timezone America/Los_Angeles --dataset-id "$DS" \
  --connection-id "$CONN" --prompt-name support-reply

# Keep the definition, stop it firing.
oodle genai datasets schedule set support-eval --enabled=false \
  --dataset-id "$DS" --connection-id "$CONN" \
  --prompt-name support-reply
```

#### Templates — `oodle genai templates`

Aliases: `template`, `library`. The judges themselves — what the UI
calls Evaluations > Library, and the API calls `eval-templates`. `list` includes Oodle-managed
templates (ids beginning `oodle-managed-`), which are
read-only.

Three `type` values: `llm` (a judge prompt), `code` (a Python
scorer), and `output_comparer` (a judge that scores the output
against a dataset item's expected output, using `{{output}}` and
`{{expected_output}}`). A comparer has ground truth only inside
an experiment, so it never runs against live traffic.

| Subcommand         | Description                    |
|--------------------|--------------------------------|
| `list`             | List evaluators                |
| `get <id>`         | Get an evaluator               |
| `create -f <file>` | Create an evaluator            |
| `update <id> -f`   | Update an evaluator            |
| `delete <id>`      | Delete an evaluator            |

#### Evaluators — `oodle genai evaluators`

Aliases: `evaluator`, `eval-rules`, `rules`. What makes a
template run against live traffic — the UI's Evaluations >
Evaluators, the API's `evaluation-rules`. An LLM template
requires an `llmConnectionId`; the server rejects an evaluator
with no model to call. Set `samplingRate` and
`maxInvocationsPerHour` before enabling one on a busy service —
an unsampled, uncapped rule is one model call per matching span.

| Subcommand         | Description                        |
|--------------------|------------------------------------|
| `list`             | List evaluation rules              |
| `create -f <file>` | Create an evaluation rule          |
| `update <id>`      | Update, or `--enable` / `--disable`|
| `delete <id>`      | Delete an evaluation rule          |

`list` reports each rule's `KIND` — the type of the template it
runs — so an output comparer can be told from an ordinary judge.
`--type` narrows the list to one kind.

```bash
oodle genai evaluators update rule_123 --disable
oodle genai evaluators list --type output_comparer
```

#### Scores — `oodle genai scores`

Alias: `score`. Scores are read out of the trace store, so
`list` defaults to the **last 15 minutes**; pass `--start` for
anything older.

| Subcommand              | Description                     |
|-------------------------|---------------------------------|
| `list`                  | List scores                     |
| `get <id>`              | Get a score                     |

```bash
oodle genai scores list --name Hallucination --start -24h --max 0.5
oodle genai scores get "$SCORE_ID"
```

#### Experiments — `oodle genai experiments`

Aliases: `experiment`, `runs`, `exp`. A run is queued and
picked up by a worker, so `run` returns as soon as the job
exists.

| Subcommand        | Description                                |
|-------------------|--------------------------------------------|
| `list <dataset>`  | List a dataset's experiment runs           |
| `items <run-id>`  | Per-item results, joined with their scores |
| `run`             | Start an experiment run                    |
| `status <job-id>` | Get a job's status                         |
| `cancel <job-id>` | Cancel a queued or running job             |
| `jobs`            | List pending jobs, or one run's history    |

Evaluators come in two kinds and each id goes in its own flag.
An ordinary judge scores the generation on its own merits
(`--evaluator-id`); an output comparer scores it against the
dataset item's expected output (`--output-comparer-id`), and
skips an item that has none. The server rejects an id put in
the wrong flag.

An evaluator judges with the model its template names, falling
back to the eval connection's default model.
`--evaluator-model` overrides that for every evaluator given by
flag — the way to judge with a cheaper model than you generate
with, without defining a rule first.

```bash
oodle genai experiments run --dataset-id "$DS" --connection-id "$CONN" \
  --prompt-name support-reply --model gpt-4o \
  --evaluator-id oodle-managed-hallucination-v1 \
  --output-comparer-id oodle-managed-output-match-v1 \
  --evaluator-model gpt-4o-mini
```

#### Connections — `oodle genai connections`

Aliases: `connection`, `conn`. Provider credentials that
evaluators and experiments call models through. Keys are
encrypted at rest and never returned, so an update that omits
`--api-key` leaves the stored key in place.

| Subcommand         | Description             |
|--------------------|-------------------------|
| `list`             | List LLM connections    |
| `create`           | Create an LLM connection|
| `update <id> -f`   | Update an LLM connection|
| `delete <id>`      | Delete an LLM connection|

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

### Grafana Migration — `oodle grafana`

Migrate a Grafana instance's dashboards, folders, data sources and alert
rules into Oodle. The command runs entirely from your machine, so it works
even when Grafana is only reachable locally (for example behind a VPN):
it exports the assets from Grafana, uploads them to Oodle, and imports them.

```sh
# Full migration (export -> upload -> import)
oodle grafana migrate \
  --grafana-url https://grafana.internal.acme.com \
  --grafana-token <grafana-service-account-token>

# Only migrate dashboards carrying specific tags
oodle grafana migrate --grafana-url ... --grafana-token ... \
  --include-tags team-a,prod

# Export and upload only, then review and import from the Oodle UI
oodle grafana migrate --grafana-url ... --grafana-token ... --skip-import
```

| Flag              | Description                                                     |
|-------------------|-----------------------------------------------------------------|
| `--grafana-url`   | Grafana base URL (required)                                     |
| `--grafana-token` | Grafana service account token (required)                       |
| `--include-tags`  | Only migrate dashboards with these tags; empty migrates all    |
| `--overwrite`     | Overwrite existing dashboards and data sources (default `true`)|
| `--skip-import`   | Export and upload only; review and import from the Oodle UI    |

Non-Prometheus data sources (CloudWatch, Cloud Monitoring, Athena, BigQuery,
Azure Monitor, ...) are recreated in Oodle with their original IDs so your
dashboards keep working; configure their credentials from the Oodle UI after
migration. Prometheus panels are repointed at Oodle's built-in data source.

### Other commands

| Command       | Description                                    |
|---------------|------------------------------------------------|
| `auth`        | OAuth authentication commands                  |
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
