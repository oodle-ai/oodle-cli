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

Monitors use PromQL queries on metrics.

- **Alerting on logs:** create a [log metrics rule](#log-metrics--oodle-log-metrics)
  that counts or measures the log lines, then create a monitor on the
  `oodle_logs_*` metric that the rule makes.
- **Alerting on traces:** monitors cannot run TraceQL. Use the
  `oodle_trace_metrics` metric, or the `oodle_genai_*` metrics for GenAI
  spans, in the monitor query.

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

A log metrics rule turns matching log lines into metrics. To alert on logs,
create a rule, then create a monitor on the metric that it makes. A rule reads
only logs that arrive after you create or change it; it does not fill in
metrics for older logs. You do not need a log transform to use a rule.

Rule fields:

| Field               | Description |
|---------------------|-------------|
| `name`              | Name of the rule. |
| `filter`            | Which log lines to read. One of: a condition `{"field", "operator", "value", "jsonPath"}`; `{"all": [...]}` (every filter must match); `{"any": [...]}` (one or more must match); or `{"not": filter}`. Items in `all`, `any` and `not` use the same forms. Operators: `is`, `contains`, `matches regex`, `exists`. |
| `labels`            | Labels for each metric. Each has a `name` and either a static `value` or a `valueExtractor` (`field`, optional `jsonPath`, optional `regex`). |
| `metricDefinitions` | Metrics to make. Each has a `name` and a `type`: `log_count`, `counter`, `gauge` or `histogram`. `counter`, `gauge` and `histogram` read the value from `field`, with an optional `jsonPath` or `regex`. |

A field is a top-level log field. Use `jsonPath` to read a nested value in a
JSON field. Regexes use Rust syntax. When a regex extracts a value, capture
group 1 is the value.

Each metric is written as `oodle_logs_<name>`. A histogram also writes the
`_bucket`, `_sum` and `_count` series.

Example: count failed logins, labelled with the user name that a regex
captures from the `message` field.

```json
{
  "name": "login-failures",
  "filter": {
    "all": [
      {"field": "service", "operator": "is", "value": "auth"},
      {"field": "message", "operator": "matches regex", "value": "login failed for user \\S+"}
    ]
  },
  "labels": [
    {"name": "user", "valueExtractor": {"field": "message", "regex": "login failed for user (\\S+)"}}
  ],
  "metricDefinitions": [
    {"name": "login_failures", "type": "log_count"}
  ]
}
```

This rule makes `oodle_logs_login_failures`. A monitor can then use
`sum by (user) (increase(oodle_logs_login_failures[5m])) > 10`.

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
| `traceql`             | Run TraceQL search and metrics queries |

```bash
oodle traces labels -o json
oodle traces list --start -1h --end now --service api
oodle traces get <trace_id> --start -1h --end now -o json
oodle traces label-values resource::service.name --start -1h
```

`list` and `get` need `--start` and `--end`.

Trace label names have a scope prefix, for example
`resource::service.name` or `span::http.method`. If you give a
name without a prefix, such as `service.name`, `label-values`
uses the scoped label when exactly one scope has it. If the
label does not exist, the command fails and suggests the
closest label names.

#### TraceQL — `oodle traces traceql`

Alias: `tql`.

| Subcommand          | Description                                        |
|---------------------|----------------------------------------------------|
| `search <query>`    | Find traces that match a TraceQL filter            |
| `metrics <query>`   | Compute time series with a TraceQL metrics query   |
| `tags`              | List attribute names to use in a query             |
| `tag-values <tag>`  | List the values of an attribute                    |

All subcommands take `--start` (default `-1h`) and `--end` (default `now`).
They accept `now`, a relative time such as `-6h`, or an epoch timestamp in
seconds (milliseconds, microseconds and nanoseconds are also detected).
`search` takes `--limit` (default 20) and `--spss`. `metrics` takes `--step`,
such as `5m`; the default is `1m`, or larger for long ranges. `tags` takes
`--scope` (`resource`, `span`, `intrinsic` or `all`).

```bash
# Traces with an error span in the api service
oodle traces traceql search '{ resource.service.name="api" && status=error }'

# Error rate per route
oodle traces traceql metrics \
  '{ resource.service.name="api" && status=error } | rate() by (span.http.route)'

# Slow tool calls, counted per tool
oodle traces traceql metrics \
  '{ name=~"execute_tool.*" && duration > 10s } | count_over_time() by (span.gen_ai.tool.name)'

# p95 span duration, as a chart
oodle traces traceql metrics \
  '{ resource.service.name="api" } | quantile_over_time(duration, .95)' -o graph

# Attribute names and values to build a query
oodle traces traceql tags --scope span
oodle traces traceql tag-values resource.service.name
```

Metrics functions: `rate`, `count_over_time`, `avg_over_time`,
`min_over_time`, `max_over_time`, `sum_over_time`, `histogram_over_time`,
`quantile_over_time`, with an optional `by (...)`.

Not supported yet: scalar filters such as `| count() > 2`, `&&` between two
spansets (`{A} && {B}`; write `{ A && B }`), structural operators between
two spansets (`>>`, `<<`, `>`, `<`, `~`, as in `{A} > {B}`), the `parent.`
and `link.` scopes, and existence checks such as `{ span.foo }` (write
`{ span.foo != nil }`). Comparisons inside one filter, such as
`{ duration > 10s }`, work.

Monitors cannot run TraceQL. To alert on traces, use PromQL on
`oodle_trace_metrics` or on the `oodle_genai_*` metrics.

### Logs — `oodle logs`

Alias: `log`.

| Subcommand        | Description                                              |
|-------------------|----------------------------------------------------------|
| `query -f <file>` | Search logs with an OpenSearch-compatible NDJSON query   |
| `index-patterns`  | List the log index patterns                              |

`query` reads an NDJSON file: a header line that selects the index, then a
line with an OpenSearch Query DSL body. The command adds a time range filter
from `--start` and `--end` (default: the last hour). The body can also have
`aggs` for aggregations. `regexp` queries are not supported.

```bash
oodle logs index-patterns
cat > errors.ndjson <<'EOF'
{"index": "logs-*"}
{"query": {"match": {"level": "error"}}, "size": 20}
EOF
oodle logs query -f errors.ndjson --start -30m -o json
```

To alert on logs, use [log metrics](#log-metrics--oodle-log-metrics).

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
| `library`       | The `oodle_eval` reference for code evaluators         |
| `code-libraries`| Your Python modules that code evaluators import        |
| `backfills`     | Run evaluators over past traffic                       |

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

Alias: `template`. The judges themselves — what the UI
calls Evaluations > Library, and the API calls `eval-templates`. `list` includes Oodle-managed
templates (ids beginning `oodle-managed-`), which are
read-only. The `CATEGORY` column groups them; the built-in code
checks show "Built-in checks". The description is in `-o yaml`.

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
| `starters [id]`    | List code starters, or print one as a template file |
| `validate -f <file>` | Check a template without saving it; exits non-zero when not valid |
| `pull <ref> <dir>` | Write a code template to local files (see below) |
| `test <dir>`       | Run the local files on one span |
| `push <dir>`       | Save the local files: shared libraries, then the template |

A starter is a ready-made code check (JSON validity, tone, PII
leak, conversation degeneration and others) or an example that
combines checks. The list names the settings of each starter.
The printed file has the settings under `params` and the source
as a YAML block. Print one, change the defaults or the code,
then create the template:

```bash
oodle genai templates starters pii-leak > pii.yaml
oodle genai templates create -f pii.yaml
```

Check a template file before you save it. The command prints
each problem, the evaluators whose scores the code reads and the
shared library versions it would run. It exits non-zero when a
save would be refused. `--rule-params` checks an evaluator's
values for the settings, and `--rule-name` names the evaluator
that would run the code, which also checks the score reads for
cycles:

`--libraries <dir>` checks the `.py` files of a directory (or one
file) as draft shared libraries, in place of the stored ones:

```bash
oodle genai templates validate -f pii.yaml --rule-params params.yaml
oodle genai templates validate -f pii.yaml --libraries shared/
oodle genai templates validate -d ./refund-check   # a pulled directory
```

The file is YAML; `-o json` prints it as JSON. It carries the
starter's primary score as `scoreType` and `higherIsBetter`, so
that a check whose verdict is bad when true (a refusal, a
degenerated conversation) is not shown as a pass. `templates get
-o yaml` also gives a file that `templates update -f` reads
back: it uses the API's key names (`libraryPins`, `sourceCode`)
and writes source code as a block.

A starter marked `BUILT-IN` is also a managed template,
`oodle-managed-code-<starter-id>-v1`. An evaluator can use it
with no code and set its settings in `params`.

A code template declares its settings in `params` (a list of
`name`, `type`, `default`, and optional `label`, `description`,
`required`, `options`). The code reads the resolved values as
`ctx.params`. `libraryPins` sets the version of a shared library
that the template runs:

```yaml
name: Mentions refund
type: code
sourceCodeLanguage: python
params:
  - name: required
    type: string_list
    default: [refund]
libraryPins:
  acme_text: 3
sourceCode: |
  from oodle_eval.v1 import metrics

  def evaluate(ctx):
      return EvaluationResult(scores=metrics.keyword_check(ctx, **ctx.params))
```

#### Write code evaluators locally

Use `pull`, `test` and `push` to write a code evaluator as real
files in Claude Code, Cursor or VS Code. The editor then has
type checks and completion for the `oodle_eval` library and for
your shared libraries, and an agent can read the library source
and run the code on real spans.

```bash
# Start from a template, a starter, or nothing.
oodle genai templates pull <template-id> ./refund-check
oodle genai templates pull starter:keyword-check ./refund-check
oodle genai templates pull new ./refund-check

# Run the local code on one span. Nothing is saved.
oodle genai templates test ./refund-check --trace <trace-id>
oodle genai templates test ./refund-check --span <trace-id>:<span-id> \
  --params params.yaml --scores scores.yaml
oodle genai templates test ./refund-check --span-file span.json   # no traffic needed

# Save it: validate, then the changed shared libraries, then the template.
oodle genai templates push ./refund-check --dry-run
oodle genai templates push ./refund-check
```

`pull` writes this layout. It refuses a directory that is not
empty unless you set `--force`. `--force` does not replace your
changes: when `evaluate.py`, `template.yaml` or a file in
`shared/` differs from the last pull or push, it lists those
files and stops. `--discard-local` replaces them, and the
changes are lost. A file in `shared/` that the server does not
have stays; the next push creates it. Nothing changes until
every file is downloaded, so a failed pull leaves the directory
as it was:

```
refund-check/
├── evaluate.py            # the code (the template's sourceCode)
├── template.yaml          # name, type, params, libraryPins, scoreType ...
├── shared/<name>.py       # each shared library: at its pin, else the latest
├── oodle_eval/**          # the library that the sandbox runs (read-only)
├── __builtins__.pyi       # Score, EvaluationResult, Scores, ctx for Pyright
├── pyrightconfig.json     # basic type checks, project root
├── README.md              # layout, sandbox rules, commands
└── .oodle/
    ├── template.yaml      # the template id that push updates
    └── shared.lock.yaml   # each library's version and hash at pull
```

- `test` sends `evaluate.py`, the `params` and `libraryPins` of
  `template.yaml`, and each file in `shared/` that is new or
  changed since the pull. It prints the scores, the error and the
  logs, and exits non-zero when the code fails.
- `push` validates with the new and changed `shared/` files as
  draft libraries, and stops on problems. It creates each new file in
  `shared/` as a shared library, and updates each changed one. It
  refuses a library that changed on the server after the pull,
  a library file based on an older version than the latest (a
  pinned library), and a changed library that was deleted on the
  server; it prints a command to compare the versions. Keep a
  copy of your change and pull with `--force --discard-local`,
  or push with `--force` to replace the server's version. It then updates the template, or creates it for a
  directory from a starter, a managed template or `new`, and
  records the new id. `--pin-libraries` pins each library that
  the code imports to its version after the push.
- For an agent: tell it to read `README.md` in the directory
  first. It lists the sandbox rules (absolute imports, the allowed
  modules, the removed builtins, the time and memory limits).

#### Evaluators — `oodle genai evaluators`

Aliases: `evaluator`, `eval-rules`, `rules`. What makes a
template run against live traffic — the UI's Evaluations >
Evaluators, the API's `evaluation-rules`. An LLM template
requires an `llmConnectionId`; the server rejects an evaluator
with no model to call. Set `samplingRate` and
`maxInvocationsPerHour` before enabling one on a busy service —
an unsampled, uncapped rule is one model call per matching span.
The cap applies to code evaluators too, where each item is one
sandbox run; 0 means no limit.

| Subcommand         | Description                        |
|--------------------|------------------------------------|
| `list`             | List evaluation rules              |
| `get <id or name>` | Get a rule with its params and score inputs |
| `create -f <file>` | Create an evaluation rule          |
| `update <id>`      | Update, or `--enable` / `--disable`|
| `delete <id>`      | Delete an evaluation rule          |

`list` reports each rule's `KIND` — the type of the template it
runs — so an output comparer can be told from an ordinary judge.
`--type` narrows the list to one kind.

For a code template with settings, `params` in the file sets
the evaluator's values, by setting name. A setting left out
takes the template's default. A code evaluator that reads other
evaluators' scores (`ctx.scores["Helpfulness"]`) runs after
them. The server finds them in the code and shows them in
`scoreInputRuleIds`; you do not set that field.

`filters` in the file limits the spans that an evaluator scores.
An attribute name has its kind as a prefix, `span::` or
`resource::`. A dotted name with no prefix is refused with 400,
because the server cannot tell a span attribute from a resource
attribute. `type` is one of `eq`, `neq`, `re`, `nre` (or `=`,
`!=`, `=~`, `!~`), `oneof`, `not_oneof`, `gt`, `gte`, `lt`,
`lte`. `oneof` and `not_oneof` take a `multi_value` list in
place of `value`. The server stores the operator in the form
that the trace store matches, so a `get` shows `0`–`5` or
`GT`/`GTE`/`LT`/`LTE` in place of the word. A number outside
`0`–`5`, or an unknown word, is refused with 400.

```json
"filters": [
  {"name": "span::gen_ai.operation.name", "type": "eq", "value": "chat"},
  {"name": "span::gen_ai.request.model", "type": "oneof",
   "multi_value": ["gpt-4o", "claude-sonnet-4"]}
]
```

```bash
oodle genai evaluators update rule_123 --disable
oodle genai evaluators list --type output_comparer
oodle genai evaluators get "Refund mentioned" -o yaml
```

#### Library reference — `oodle genai library`

Shows the `oodle_eval` Python library that code evaluators
import: the built-in checks (`metrics`), the text helpers
(`text`) and the functions that combine scores (`combine`).
With no argument, a table of each function and its summary.
With a name, its signature, documentation, score names and
`file:line`; a runtime class also lists its methods. The
command prints text unless you set `-o`: `-o csv` gives the
FUNCTION and SUMMARY columns, and `-o json` or `-o yaml` the
manifest data.

```bash
oodle genai library
oodle genai library metrics.keyword_check   # or: keyword_check
oodle genai library metrics.format          # one module
oodle genai library -o json                 # the full manifest
oodle genai library keyword_check --source  # the Python source
oodle genai library files                   # the library's files
oodle genai library files oodle_eval/v1/metrics/format.py
```

A function or runtime class shows its `file:line` in the
library. `--source` prints its source: the decorators, the
definition and the body up to the next top-level statement. For
a module, `--source` prints the whole file; for a package such
as `metrics`, its `__init__.py`. A check that a package lists
again from its submodule (`metrics.keyword_check` and
`metrics.format.keyword_check`) is one entry.

#### Shared libraries — `oodle genai code-libraries`

Your own Python modules, written one time and imported from
many code evaluators as `shared.<name>`. Each change of the
source adds a version. Commands that take `<library>` accept the
id or the name.

| Subcommand                 | Description                                   |
|----------------------------|-----------------------------------------------|
| `list`                     | List shared libraries                         |
| `get <library>`            | Get one, with the templates that import it    |
| `create`                   | Create from `--source file.py` (or `-` for standard input) and/or `-f` |
| `update <library>`         | Change the source or description; `--restore N` saves version N as a new version |
| `delete <library>`         | Delete it; refused while anything imports it  |
| `versions <library>`       | List versions; `--version N` prints that source |

```bash
oodle genai code-libraries create --source acme_text.py \
  --description "Text helpers for the support agent"
oodle genai code-libraries update acme_text --source acme_text.py
oodle genai code-libraries versions acme_text --version 1 > v1.py
oodle genai code-libraries update acme_text --restore 1
cat acme_text.py | oodle genai code-libraries update acme_text --source -
```

An update with an empty source, or with nothing to change, is
refused before any request is sent.

`get` and a refused `delete` (409) name what imports the
library, each with its kind: `template`, or `library` for
another shared library. Either kind blocks a delete.

A restore does not remove versions: `--restore N` saves the
source of version N as the new latest version.

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

To test your own agent rather than a model, run against a
webhook instead of a connection. No prompt or model is read
then; the endpoint owns both. LLM judges still need a
connection of their own (`--eval-connection-id`), since there
is no generation connection to fall back to.

```bash
oodle genai experiments run --dataset-id "$DS" --webhook-id "$WH" \
  --output-comparer-id oodle-managed-output-match-v1 \
  --eval-connection-id "$CONN"
```

An `evaluatorRules` entry in a `--file` config can name the
rules it depends on in `dependsOnRuleIds`. With
`--honor-dependencies`, a dependent evaluator scores an item
only where each evaluator it depends on reported a finding.
The server refuses the run (400) when a rule depends on a rule
that is not in the run, or when the rules form a cycle.

#### Backfills — `oodle genai backfills`

Alias: `backfill`. Run evaluators over traces that already
arrived. A run is queued, so `create` returns at once.

| Subcommand      | Description                                    |
|-----------------|------------------------------------------------|
| `list`          | List runs, with their status                   |
| `get <id>`      | Get a run                                      |
| `create`        | Start a run from flags and/or `-f`             |
| `cancel <id>`   | Cancel a queued or running run                 |
| `delete <id>`   | Delete a finished run from the list            |

```bash
oodle genai backfills create --name "Refund check, last week" \
  --evaluator-id "$RULE" --start -7d --sample-rate 0.1
oodle genai backfills list
```

The window cannot end in the future and is at most 90 days.
When an evaluator of the run reads other evaluators' scores,
the run adds them too, and `create` names them in `alsoRuns`.

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

#### Webhooks — `oodle genai webhooks`

Alias: `webhook`. Endpoints you host that run your own agent
or workflow. An experiment run against one POSTs every dataset
item to it, using the webhook's request template, reads the
output out of the reply at its output path, and scores it. The
request carries a W3C `traceparent` header, so a service with
OpenTelemetry HTTP instrumentation links its trace to each
result. Headers are encrypted at rest and never returned.

| Subcommand         | Description                                    |
|--------------------|------------------------------------------------|
| `list`             | List webhooks                                  |
| `get <id>`         | Get a webhook                                  |
| `create`           | Create a webhook                               |
| `update <id> -f`   | Update a webhook from a file                   |
| `delete <id>`      | Delete a webhook                               |
| `test <id>`        | Send one request the way a run would           |

The request template is JSON with `{{path}}` placeholders read
from the item (`{{input}}`, `{{input.<field>}}`,
`{{metadata.<field>}}`, `{{id}}`) and the run (`{{run.id}}`,
`{{run.name}}`, `{{dataset.id}}`, `{{dataset.name}}`),
inserted as JSON; the default `{{input}}` sends the item's
input as the body. Every request carries a W3C `traceparent`
header and `X-Oodle-Experiment` with the run id. The
output path is a dot path over the reply, such as `answer` or
`choices[0].message.content`; empty stores the whole reply.

```bash
oodle genai webhooks create --name "Support agent" \
  --url https://agent.example.com/run \
  --header "Authorization=Bearer $AGENT_TOKEN" \
  --request-template '{"query": {{input.question}}}' \
  --output-path answer --timeout 120
oodle genai webhooks test "$WH" --input '{"question": "Is checkout slow?"}'
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
