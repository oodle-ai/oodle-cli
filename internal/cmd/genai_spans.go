package cmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// This file holds the GenAI span logic that the trace read commands
// share: the filter flags and the reading of GenAI attributes from span
// tags.

// genaiOperationLabel is on every GenAI span. A filter on it keeps
// ordinary application traces out of the results. Without it, a read
// on an instance with mixed traffic returns mostly non-GenAI traces.
const genaiOperationLabel = "span::gen_ai.operation.name"

// genaiFilterLabels maps the filter names of the GenAI commands to
// their trace labels. The names are the same as the flag names.
var genaiFilterLabels = map[string]string{
	"env":       "resource::env",
	"service":   "resource::service.name",
	"model":     "span::gen_ai.request.model",
	"agent":     "span::gen_ai.agent.name",
	"operation": genaiOperationLabel,
	"tool":      "span::gen_ai.tool.name",
	"user":      "span::user.id",
	"session":   "span::gen_ai.conversation.id",
	"signal":    "span::oodle.signal.execution",
	"score":     "span::score.name",
}

// genaiFilterOrder is the order in which the filter flags are
// registered and shown in help.
var genaiFilterOrder = []string{
	"service", "agent", "model", "tool", "operation",
	"user", "session", "env", "signal",
}

// genaiLabelFor returns the trace label for a field name. A built-in
// name maps to its label, a scoped name ("resource::x") is used as
// it is, and any other name is a span attribute.
func genaiLabelFor(field string) string {
	if l, ok := genaiFilterLabels[field]; ok {
		return l
	}
	if strings.Contains(field, "::") {
		return field
	}
	return "span::" + field
}

// traceMatcher is one entry of the traces API "filters" parameter.
// Type is 0 (equal) or 2 (regular expression).
type traceMatcher struct {
	Name  string `json:"name"`
	Type  int    `json:"type"`
	Value string `json:"value"`
}

// genaiFilterFlags holds the filter flags of the GenAI read commands.
type genaiFilterFlags struct {
	values map[string]*[]string
	attrs  []string
}

// addTo registers the filter flags on cmd. Each flag can be given
// more than once, and several values match any of them.
func (f *genaiFilterFlags) addTo(cmd *cobra.Command) {
	help := map[string]string{
		"service":   "Service name (resource service.name)",
		"agent":     "Agent name (gen_ai.agent.name)",
		"model":     "Requested model (gen_ai.request.model)",
		"tool":      "Tool name (gen_ai.tool.name)",
		"operation": "GenAI operation, such as chat, execute_tool or invoke_agent",
		"user":      "End-user ID (user.id)",
		"session":   "Session or conversation ID (gen_ai.conversation.id)",
		"env":       "Environment (resource env)",
		"signal":    "Execution signal, such as failure.tool_error",
	}
	f.values = map[string]*[]string{}
	for _, name := range genaiFilterOrder {
		v := []string{}
		f.values[name] = &v
		cmd.Flags().StringSliceVar(&v, name, nil, help[name]+"; repeat or comma-separate to match any")
	}
	cmd.Flags().StringArrayVar(&f.attrs, "attr", nil,
		"Custom span attribute as key=value (repeatable). Use resource::key for a resource attribute")
}

// matchers returns the filters, always with the GenAI operation
// filter first unless --operation replaces it.
func (f *genaiFilterFlags) matchers() ([]traceMatcher, error) {
	out := []traceMatcher{}
	if f == nil || f.values == nil || len(*f.values["operation"]) == 0 {
		out = append(out, traceMatcher{Name: genaiOperationLabel, Type: 2, Value: ".+"})
	}
	if f == nil || f.values == nil {
		return out, nil
	}
	for _, name := range genaiFilterOrder {
		if m, ok := valuesMatcher(genaiFilterLabels[name], *f.values[name]); ok {
			out = append(out, m)
		}
	}
	for _, a := range f.attrs {
		k, v, ok := strings.Cut(a, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("--attr %q: expected key=value", a)
		}
		out = append(out, traceMatcher{Name: genaiLabelFor(k), Type: 0, Value: v})
	}
	return out, nil
}

// valuesMatcher returns an equality matcher for one value, and a
// regular expression for several. The server anchors a pattern only
// by adding "^" and "$", so an alternation without a group would match
// "starts with a OR ends with b". Each value is quoted so that a dot
// in a model name matches only a dot.
func valuesMatcher(label string, values []string) (traceMatcher, bool) {
	var vs []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			vs = append(vs, v)
		}
	}
	switch len(vs) {
	case 0:
		return traceMatcher{}, false
	case 1:
		return traceMatcher{Name: label, Type: 0, Value: vs[0]}, true
	}
	quoted := make([]string, len(vs))
	for i, v := range vs {
		quoted[i] = regexp.QuoteMeta(v)
	}
	return traceMatcher{Name: label, Type: 2, Value: "(?:" + strings.Join(quoted, "|") + ")"}, true
}

// genaiExtraFields are the span attributes that a trace search
// returns in addition to its base columns. The projection is by
// exact column name, so each number is listed with its _int and
// _float forms; a missing form reads as zero tokens.
var genaiExtraFields = func() string {
	fields := []string{
		"gen_ai.operation.name", "langfuse.observation.type",
		"gen_ai.request.model", "gen_ai.agent.name", "gen_ai.tool.name",
		"user.id", "gen_ai.conversation.id", "gen_ai.session.id",
		"oodle.signal.execution", "score.name", "score.string_value",
	}
	numbers := []string{
		"gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens",
		"gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens",
		"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens",
		"gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_creation_input_tokens",
		"gen_ai.usage.cost", "gen_ai.cost.total_cost", "score.value",
	}
	for _, n := range numbers {
		fields = append(fields, n, n+"_int", n+"_float")
	}
	return strings.Join(fields, ",")
}()

// --- Trace response ---

type genaiTracesResponse struct {
	Data []genaiTrace `json:"data"`
	// RowsRead reaches RowLimit when the server stops the read
	// early. The traces are then possibly incomplete.
	RowsRead int64 `json:"rowsRead"`
	RowLimit int64 `json:"rowLimit"`
}

type genaiTrace struct {
	TraceID   string                       `json:"traceID"`
	Spans     []genaiSpan                  `json:"spans"`
	Processes map[string]genaiTraceProcess `json:"processes"`
}

type genaiTraceProcess struct {
	ServiceName string `json:"serviceName"`
}

type genaiSpan struct {
	SpanID        string    `json:"spanID"`
	OperationName string    `json:"operationName"`
	StartTime     int64     `json:"startTime"`
	Duration      int64     `json:"duration"`
	ProcessID     string    `json:"processID"`
	Tags          []spanTag `json:"tags"`
}

type spanTag struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// spanTags is a span's tags by key. The first value of a key wins.
type spanTags map[string]any

func tagsOf(s genaiSpan) spanTags {
	t := make(spanTags, len(s.Tags))
	for _, tag := range s.Tags {
		if _, ok := t[tag.Key]; !ok && tag.Key != "" {
			t[tag.Key] = tag.Value
		}
	}
	return t
}

// str returns a tag as a string, with "" for a missing tag.
func (t spanTags) str(key string) string {
	v, ok := t[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// num reads a number in each form that ingest writes: the plain
// name, and the _int and _float columns. Some sources send every
// number as a float or as a string. A form that is not read gives
// zero tokens and zero cost.
func (t spanTags) num(key string) (float64, bool) {
	for _, k := range []string{key, key + "_int", key + "_float"} {
		switch v := t[k].(type) {
		case float64:
			return v, true
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// firstNum returns the number of the first key that has one. The
// counts are not added: ingest can write one count under two names.
func (t spanTags) firstNum(keys ...string) float64 {
	for _, k := range keys {
		if v, ok := t.num(k); ok {
			return v
		}
	}
	return 0
}

func (t spanTags) firstStr(keys ...string) string {
	for _, k := range keys {
		if v := t.str(k); v != "" {
			return v
		}
	}
	return ""
}

// hasError is true when the span has the error tag or an error status.
func (t spanTags) hasError() bool {
	switch v := t["error"].(type) {
	case bool:
		if v {
			return true
		}
	case string:
		if strings.EqualFold(v, "true") {
			return true
		}
	}
	return strings.Contains(strings.ToLower(t.str("otel.status_code")), "error")
}

// evalOperations are spans that the evaluation pipeline writes. They
// look like model calls, but they are not agent traffic.
var evalOperations = map[string]bool{
	"eval_score":          true,
	"dataset_run_item":    true,
	"code_eval_execution": true,
}

// langfuseOperations maps a Langfuse observation type to the
// operation name that it means.
var langfuseOperations = map[string]string{
	"generation": "chat",
	"llm":        "chat",
	"embedding":  "embeddings",
	"agent":      "invoke_agent",
	"tool":       "execute_tool",
}

// spanOperation returns the GenAI operation of a span. The type that
// the producer gives wins over the span name: a span that a developer
// named "chat" is not always a model call.
func spanOperation(t spanTags, s genaiSpan) string {
	if op := t.str("gen_ai.operation.name"); op != "" {
		return op
	}
	if lt := t.str("langfuse.observation.type"); lt != "" {
		return langfuseOperations[strings.ToLower(lt)]
	}
	return s.OperationName
}

// isLeafLLMCall is true when the token counts of a span are its own.
// An agent or workflow span repeats the counts of its children, so a
// sum over all spans counts the same tokens more than once.
func isLeafLLMCall(op string) bool {
	if evalOperations[op] {
		return false
	}
	switch op {
	case "text_completion", "embeddings", "evaluate":
		return true
	}
	return strings.HasPrefix(op, "chat") ||
		strings.HasPrefix(op, "generate_content") ||
		strings.HasPrefix(op, "jev.")
}

// stepType returns AGENT, TOOL, GENERATION or SPAN for an operation.
func stepType(op string) string {
	switch {
	case strings.HasPrefix(op, "invoke_agent"), strings.HasPrefix(op, "invocation"):
		return "AGENT"
	case strings.HasPrefix(op, "execute_tool"):
		return "TOOL"
	case isLeafLLMCall(op):
		return "GENERATION"
	}
	return "SPAN"
}

// tokenUsage holds the token classes of one or more model calls.
// The classes do not overlap: Input is the part of the prompt that
// is not cached, so the total is the sum of all four.
type tokenUsage struct {
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
}

func (u tokenUsage) total() int64 { return u.Input + u.CacheRead + u.CacheWrite + u.Output }

func (u *tokenUsage) add(o tokenUsage) {
	u.Input += o.Input
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Output += o.Output
}

// usageOf reads the tokens of one model call. Claude models report
// the input without the cached part. Other providers include it, so
// the cached part is removed to make the classes disjoint. When the
// cache is larger than the input, the input cannot include it.
func usageOf(t spanTags) tokenUsage {
	in := int64(t.firstNum("gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens"))
	cr := int64(t.firstNum("gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens"))
	cw := int64(t.firstNum("gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_creation_input_tokens"))
	out := int64(t.firstNum("gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens"))
	exclusive := strings.Contains(strings.ToLower(t.str("gen_ai.request.model")), "claude") || cr+cw > in
	if !exclusive {
		in = max(in-cr-cw, 0)
	}
	return tokenUsage{Input: in, CacheRead: cr, CacheWrite: cw, Output: out}
}

// reportedCost returns the cost that the instrumentation reported.
func reportedCost(t spanTags) (float64, bool) {
	for _, k := range []string{"gen_ai.usage.cost", "gen_ai.cost.total_cost"} {
		if v, ok := t.num(k); ok && v != 0 {
			return v, true
		}
	}
	return 0, false
}

// genaiScore is one evaluation score read from an evaluation span.
type genaiScore struct {
	Name        string   `json:"name"`
	Value       *float64 `json:"value,omitempty"`
	StringValue string   `json:"string_value,omitempty"`
}

func scoreOf(t spanTags) (genaiScore, bool) {
	name := t.str("score.name")
	if name == "" {
		return genaiScore{}, false
	}
	s := genaiScore{Name: name, StringValue: t.str("score.string_value")}
	if v, ok := t.num("score.value"); ok {
		s.Value = &v
	}
	return s, true
}

// formatUTC formats epoch microseconds as a UTC time, or "" for zero.
func formatUTC(us int64) string {
	if us <= 0 {
		return ""
	}
	return time.UnixMicro(us).UTC().Format(time.RFC3339)
}

// formatDurationMs prints a duration in milliseconds rounded for a
// table cell.
func formatDurationMs(ms float64) string {
	d := time.Duration(ms * float64(time.Millisecond))
	switch {
	case d >= time.Second:
		return d.Round(10 * time.Millisecond).String()
	case d >= time.Millisecond:
		return d.Round(time.Millisecond).String()
	}
	return d.String()
}

// formatCost prints a cost in dollars, or "" for no cost.
func formatCost(c float64) string {
	if c == 0 {
		return ""
	}
	if c >= 0.01 {
		return "$" + strconv.FormatFloat(c, 'f', 2, 64)
	}
	return "$" + strconv.FormatFloat(c, 'f', 5, 64)
}

// sortedKeys returns the keys of a set in order.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// joinCell joins a list for a table cell. Long lists show the first
// items and a count of the others, so that the table stays readable.
func joinCell(vs []string) string {
	const n = 3
	if len(vs) <= n {
		return strings.Join(vs, ",")
	}
	return strings.Join(vs[:n], ",") + fmt.Sprintf(",+%d", len(vs)-n)
}
