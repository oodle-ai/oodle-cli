package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newGenAICmd returns the `oodle genai` command tree: the
// evaluation and prompt-management side of Agent Observability.
// Reading GenAI telemetry stays under `oodle traces` and
// `oodle metrics`.
func newGenAICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "genai",
		Aliases: []string{"llmops", "ai"},
		Short:   "Manage GenAI prompts, datasets, evaluators, and experiments",
		Long: `Manage the evaluation side of Oodle Agent Observability.

  prompts      versioned prompts, resolved by label
  datasets     evaluation datasets and their items
  evaluators   LLM-as-judge and code evaluators
  eval-rules   run evaluators over live traffic
  scores       evaluator output and manual scores
  experiments  run a prompt over a dataset and score it
  connections  provider credentials evaluators run against

A first run, end to end:

  oodle genai connections create -f openai.json
  oodle genai datasets create -f dataset.json
  oodle genai evaluators create -f judge.json
  oodle genai experiments run --dataset-id <id> \
    --prompt-name my-prompt --connection-id <id> --model gpt-4o`,
	}

	cmd.AddCommand(newGenAIPromptsCmd())
	cmd.AddCommand(newGenAIDatasetsCmd())
	cmd.AddCommand(newGenAIEvaluatorsCmd())
	cmd.AddCommand(newGenAIEvalRulesCmd())
	cmd.AddCommand(newGenAIScoresCmd())
	cmd.AddCommand(newGenAIExperimentsCmd())
	cmd.AddCommand(newGenAIConnectionsCmd())

	return cmd
}

// genaiCheck turns a non-2xx status into the shared API error.
//
// It takes the response's parts rather than the response
// itself because the generated *WithResponse types expose
// Body and HTTPResponse as fields, not methods, so there is no
// interface they all satisfy. Callers must handle the transport
// error before calling this — on transport failure the response
// pointer is nil.
func genaiCheck(
	status int,
	httpResp *http.Response,
	body []byte,
) error {
	if status >= 300 {
		return api.CheckResponse(httpResp, body)
	}
	return nil
}

// errEmptyResponse is returned when the server answered 2xx but
// the body did not decode into the expected shape.
var errEmptyResponse = errors.New("unexpected empty response")

// pageFlags holds the pagination flags shared by the GenAI list
// commands.
type pageFlags struct {
	limit int
	page  int
}

// addTo registers --limit and --page on cmd.
func (p *pageFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().IntVar(
		&p.limit, "limit", 0,
		"Results per page (server default 50, max 200)",
	)
	cmd.Flags().IntVar(
		&p.page, "page", 0,
		"1-based page number",
	)
}

// values returns the flags as the optional pointers the
// generated params expect. Zero means "not supplied", so the
// server's own default applies rather than a limit of 0.
func (p *pageFlags) values() (limit, page *int) {
	if p.limit > 0 {
		limit = &p.limit
	}
	if p.page > 0 {
		page = &p.page
	}
	return limit, page
}

// optStr returns a pointer to s, or nil when s is empty, for
// the many optional string params where "unset" and "empty"
// mean different things to the server.
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// toRFC3339 normalizes a timestamp flag for the llmops
// endpoints, which take RFC3339 rather than the epoch the rest
// of the API uses.
//
// It accepts the same relative forms as every other time flag
// in this CLI ("-24h", "-7d", "now") so these do not need their
// own mental model, and passes an explicit RFC3339 value
// through untouched. An unparseable value is an error rather
// than a pass-through: sent verbatim the server ignores it, and
// a `--start -24h` that silently means "last 15 minutes" reads
// as "nothing was scored yesterday".
func toRFC3339(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if strings.EqualFold(v, "now") {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	if v[0] == '+' || v[0] == '-' {
		dur, err := parseRelativeDuration(v)
		if err != nil {
			return "", timeFlagError(value)
		}
		return time.Now().UTC().Add(dur).Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return "", timeFlagError(value)
	}
	return v, nil
}

func timeFlagError(value string) error {
	return fmt.Errorf(
		"invalid time %q: expected RFC3339 "+
			"(2026-08-12T00:00:00Z), 'now', or a relative "+
			"duration like -24h or -7d",
		value,
	)
}

// printGenAI writes data using the shared formatter. Columns
// only affect table and CSV output; json and yaml always print
// the full object.
func printGenAI(
	cmd *cobra.Command,
	data any,
	columns []output.Column,
) error {
	return output.Print(
		cmd.OutOrStdout(), getOutputFormat(cmd), data, columns,
	)
}

// deref returns the value behind p, or the zero value of T when
// p is nil. Generated list envelopes hold `*[]T`, and a nil
// slice pointer and an empty list mean the same thing to every
// caller here.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
