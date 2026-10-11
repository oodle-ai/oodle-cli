package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newGenAICmd returns the `oodle genai` command tree: the
// evaluation and prompt-management side of Agent Observability,
// and reads of GenAI traces. GenAI metrics stay under
// `oodle metrics`.
func newGenAICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "genai",
		Aliases: []string{"llmops", "ai"},
		Short:   "Read GenAI traces; manage prompts, datasets, evaluators, and experiments",
		Long: `Read LLM and agent traces, and manage the evaluation side of Oodle
Agent Observability.

  traces       search LLM and agent traces, sessions and users
  trace        one trace as a list of agent, model and tool steps
  values       the fields and values to filter GenAI traces by
  agent-graph  agents, tools and model calls, and the calls between them
  recommendations
               findings about the quality, cost and speed of your agents

  prompts      versioned prompts, resolved by label
  datasets     evaluation datasets and their items
  templates    LLM-as-judge and code judges (Evaluations > Library in the UI)
  evaluators   run templates over live traffic
  scores       evaluator output (read-only)
  experiments  run a prompt over a dataset and score it
  backfills    run evaluators over past traffic
  connections  provider credentials evaluators run against
  library      the oodle_eval reference for code evaluators
  code-libraries
               your Python modules that code evaluators import
               as shared.<name>

A first run, end to end:

  oodle genai connections create -f openai.json
  oodle genai datasets create -f dataset.json
  oodle genai templates create -f judge.json
  oodle genai experiments run --dataset-id <id> \
    --prompt-name my-prompt --connection-id <id> --model gpt-4o`,
	}

	cmd.AddCommand(newGenAITracesCmd())
	cmd.AddCommand(newGenAITraceCmd())
	cmd.AddCommand(newGenAIValuesCmd())
	cmd.AddCommand(newGenAIAgentGraphCmd())
	cmd.AddCommand(newGenAIRecommendationsCmd())
	cmd.AddCommand(newGenAIPromptsCmd())
	cmd.AddCommand(newGenAIDatasetsCmd())
	cmd.AddCommand(newGenAITemplatesCmd())
	cmd.AddCommand(newGenAIEvaluatorsCmd())
	cmd.AddCommand(newGenAIScoresCmd())
	cmd.AddCommand(newGenAIExperimentsCmd())
	cmd.AddCommand(newGenAIConnectionsCmd())
	cmd.AddCommand(newGenAIWebhooksCmd())
	cmd.AddCommand(newGenAIBackfillsCmd())
	cmd.AddCommand(newGenAILibraryCmd())
	cmd.AddCommand(newGenAICodeLibrariesCmd())

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
	if status < 300 {
		return nil
	}
	err := api.CheckResponse(httpResp, body)
	var apiErr *api.APIError
	if status != http.StatusUnauthorized && errors.As(err, &apiErr) {
		if msg, details := genaiErrorMessage(body); msg != "" {
			apiErr.Message = msg
			apiErr.Details = details
		}
	}
	return err
}

// genaiErrorBody is the error shape of the GenAI API. It
// is not the Oodle error envelope, so without this decode the
// user sees the raw JSON instead of the message.
type genaiErrorBody struct {
	Message    string        `json:"message"`
	Error      string        `json:"error"`
	UsedBy     []genaiRefRow `json:"usedBy"`
	Dependents []genaiRefRow `json:"dependents"`
}

type genaiRefRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is "template" or "library" in a library's used-by
	// list. A library that only another library imports also
	// blocks a delete, and without the kind the user looks for
	// it among the templates.
	Kind string `json:"kind"`
}

// genaiErrorMessage returns the message of a GenAI error body,
// and as details the objects that block the change. A refused
// delete, rename or template update is only useful when the user
// can see which templates, libraries or evaluators to change
// first. A list whose names the message already gives is left
// out, so that no name shows twice. msg is "" when the body is
// not a GenAI error.
func genaiErrorMessage(body []byte) (msg, details string) {
	var e genaiErrorBody
	if len(body) == 0 || json.Unmarshal(body, &e) != nil {
		return "", ""
	}
	msg = e.Message
	if msg == "" {
		msg = e.Error
	}
	if msg == "" {
		return "", ""
	}
	var parts []string
	writeRefs := func(title string, refs []genaiRefRow) {
		if len(refs) == 0 || allNamed(msg, refs) {
			return
		}
		var b strings.Builder
		b.WriteString(title + ":")
		for _, r := range refs {
			b.WriteString("\n  - ")
			if r.Kind != "" {
				b.WriteString(r.Kind + " ")
			}
			fmt.Fprintf(&b, "%s (%s)", r.Name, r.ID)
		}
		parts = append(parts, b.String())
	}
	writeRefs("Used by", e.UsedBy)
	writeRefs("Affected evaluators", e.Dependents)
	return msg, strings.Join(parts, "\n")
}

// allNamed is true when msg contains the name of each ref.
func allNamed(msg string, refs []genaiRefRow) bool {
	for _, r := range refs {
		if r.Name == "" || !strings.Contains(msg, r.Name) {
			return false
		}
	}
	return true
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
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		// An epoch, as the other commands take. The unit is found from
		// the magnitude.
		ns := epochToUnit(n, 1e9)
		return time.Unix(0, ns).UTC().Format(time.RFC3339), nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return "", timeFlagError(value)
	}
	return v, nil
}

func timeFlagError(value string) error {
	return fmt.Errorf(
		"invalid time %q: expected RFC3339 "+
			"(2026-08-12T00:00:00Z), 'now', a relative "+
			"duration like -24h or -7d, or an epoch",
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

// printPlain prints v through its JSON form. The generated
// types have only json tags, so YAML of the type itself has
// lower-case keys such as "sourcecode" and a null for each unset
// field. The JSON form keeps the API's key names, which makes the
// YAML a file that the create and update commands read back. The
// YAML form also leaves out null values.
func printPlain(cmd *cobra.Command, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding the response: %w", err)
	}
	var plain any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return fmt.Errorf("encoding the response: %w", err)
	}
	if getOutputFormat(cmd) == output.FormatYAML {
		plain = dropNulls(plain)
	}
	return printGenAI(cmd, plain, nil)
}

// dropNulls removes the map entries whose value is null. In a
// YAML file that the user edits, a "key: null" line is noise, and
// the create and update commands read a missing key and a null
// the same way.
func dropNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if e == nil {
				delete(t, k)
				continue
			}
			t[k] = dropNulls(e)
		}
	case []any:
		for i, e := range t {
			t[i] = dropNulls(e)
		}
	}
	return v
}

// printGenAIObject prints columns for table and CSV output and
// the full object in the API's own key names for JSON and YAML.
// The YAML must be a file that the create and update commands
// read back, so it cannot use the generated type's lower-case
// keys (see printPlain).
func printGenAIObject(
	cmd *cobra.Command,
	data any,
	columns []output.Column,
) error {
	if isTabular(cmd) {
		return printGenAI(cmd, data, columns)
	}
	return printPlain(cmd, data)
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
