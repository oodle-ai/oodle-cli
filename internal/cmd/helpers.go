package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// Default time-range and resolution values applied when --start, --end, or
// --step are omitted from exploration commands (e.g. metrics names/labels,
// logs query, query-range).
const (
	defaultStartOffset = "-1h"
	defaultEndValue    = "now"
	defaultStep        = "60s"

	// defaultHistoryStartOffset mirrors the monitor history endpoint's own
	// documented default window (last 7 days), used to fill in the missing
	// bound when only one side of the range is supplied.
	defaultHistoryStartOffset = "-7d"
)

// exactArgs returns a cobra.PositionalArgs validator that requires exactly n
// arguments, producing a user-friendly error message that includes the
// command's Use line so the user can see the expected syntax.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		if len(args) < n {
			return fmt.Errorf("missing required argument(s). Usage: %s", cmd.UseLine())
		}
		return fmt.Errorf("too many arguments. Usage: %s", cmd.UseLine())
	}
}

// ctxKey is an unexported type for context keys defined in this package.
type ctxKey int

const (
	ctxKeyClient ctxKey = iota
	ctxKeyOutput
	ctxKeyInstance
)

// withClient returns a copy of ctx that carries the given API client.
func withClient(ctx context.Context, c *api.Client) context.Context {
	return context.WithValue(ctx, ctxKeyClient, c)
}

// withOutput returns a copy of ctx that carries the desired output format.
func withOutput(ctx context.Context, f output.Format) context.Context {
	return context.WithValue(ctx, ctxKeyOutput, f)
}

// withInstance returns a copy of ctx carrying the instance ID.
func withInstance(ctx context.Context, instance string) context.Context {
	return context.WithValue(ctx, ctxKeyInstance, instance)
}

// getClient returns the API client previously stored on the command context.
func getClient(cmd *cobra.Command) *api.Client {
	if v, ok := cmd.Context().Value(ctxKeyClient).(*api.Client); ok {
		return v
	}
	return nil
}

// getOutputFormat returns the resolved output format from the command context.
func getOutputFormat(cmd *cobra.Command) output.Format {
	if v, ok := cmd.Context().Value(ctxKeyOutput).(output.Format); ok && v != "" {
		return v
	}
	return output.FormatTable
}

// getInstance returns the instance ID from the command context.
func getInstance(cmd *cobra.Command) string {
	if v, ok := cmd.Context().Value(ctxKeyInstance).(string); ok {
		return v
	}
	return ""
}

// readInputFile reads JSON or YAML from path into v. The format is auto
// detected from the file extension; unknown extensions fall back to YAML
// (which also accepts JSON).
func readInputFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, v); err != nil {
			return fmt.Errorf("parsing JSON from %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := unmarshalYAMLAsJSON(data, v); err != nil {
			return fmt.Errorf("parsing YAML from %s: %w", path, err)
		}
	default:
		// YAML is a superset of JSON; try yaml first, fall back to json.
		if err := unmarshalYAMLAsJSON(data, v); err != nil {
			if jerr := json.Unmarshal(data, v); jerr != nil {
				return fmt.Errorf("parsing %s (tried YAML and JSON): %w", path, err)
			}
		}
	}
	return nil
}

// readInputFileJSON reads a JSON or YAML file and returns it as the bytes of
// one JSON object. It uses the same format rules as readInputFile. A JSON
// file is returned without change.
//
// Use it for request bodies whose shape the generated types cannot hold.
// Decoding such a file into a generated type drops the fields that the type
// does not know, and the server then gets a different object than the file.
func readInputFileJSON(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	ext := strings.ToLower(filepath.Ext(path))
	var out []byte
	switch {
	case ext == ".json":
		if !json.Valid(data) {
			var v any
			err := json.Unmarshal(data, &v)
			return nil, fmt.Errorf("parsing JSON from %s: %w", path, err)
		}
		out = data
	case ext == ".yaml" || ext == ".yml":
		if out, err = yamlToJSON(data); err != nil {
			return nil, fmt.Errorf("parsing YAML from %s: %w", path, err)
		}
	case json.Valid(data):
		out = data
	default:
		if out, err = yamlToJSON(data); err != nil {
			return nil, fmt.Errorf("parsing %s (tried YAML and JSON): %w", path, err)
		}
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s must contain one JSON or YAML object", path)
	}
	return trimmed, nil
}

// yamlToJSON converts a YAML document to JSON.
func yamlToJSON(data []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("converting YAML to JSON: %w", err)
	}
	return out, nil
}

// unmarshalYAMLAsJSON decodes YAML by transcoding it to JSON first, so the
// target is filled through its JSON decoding path.
//
// Decoding YAML straight into v goes through gopkg.in/yaml.v3, which honours
// neither `json` struct tags nor json.Unmarshaler. The generated API types have
// only `json` tags, and their oneOf unions (integration typeSpecificData, for
// one) carry their payload in a json.RawMessage populated by a custom
// UnmarshalJSON. Decoded as YAML those unions silently come back empty, so a
// --file request would drop its entire type-specific body and still be accepted
// by the server as an unconfigured integration.
func unmarshalYAMLAsJSON(data []byte, v any) error {
	var intermediate any
	if err := yaml.Unmarshal(data, &intermediate); err != nil {
		return err
	}
	encoded, err := json.Marshal(intermediate)
	if err != nil {
		return fmt.Errorf("converting YAML to JSON: %w", err)
	}
	return json.Unmarshal(encoded, v)
}

// parseTimeFlag converts a time flag value to epoch microseconds. Accepted
// forms:
//
//   - "now"            => current time
//   - "-1h", "-30m"    => relative durations (Go's time.ParseDuration)
//   - "-7d"            => days; converted to hours
//   - RFC3339          => for example 2026-01-02T15:04:05Z
//   - integer          => epoch in s, ms, µs or ns (see epochToUnit)
//
// See parseTimeFlagMs for the millisecond-precision variant used by
// endpoints that expect epoch ms (e.g. metrics).
func parseTimeFlag(value string) (int64, error) {
	return parseTimeFlagAs(value, "microseconds", time.Time.UnixMicro)
}

// parseTimeFlagMs is like parseTimeFlag but returns epoch milliseconds.
// Use this for endpoints (e.g. metrics) that expect millisecond timestamps.
func parseTimeFlagMs(value string) (int64, error) {
	return parseTimeFlagAs(value, "milliseconds", time.Time.UnixMilli)
}

// parseTimeFlagSec is like parseTimeFlag but returns whole epoch seconds.
// Use this for endpoints (e.g. the monitor history range) that expect
// integral second timestamps. This differs from parseTimeFlagSeconds, which
// returns a float64 for the Prometheus API's sub-second precision.
func parseTimeFlagSec(value string) (int64, error) {
	return parseTimeFlagAs(value, "seconds", time.Time.Unix)
}

// parseTimeFlagAs is the shared core for parseTimeFlag and parseTimeFlagMs.
// unitName is the human-readable unit used in error messages ("microseconds",
// "milliseconds"). toEpoch converts a time.Time to the desired epoch unit
// (e.g. time.Time.UnixMicro). An integer literal is converted to the
// requested unit by its magnitude (see epochToUnit), so a value that is
// already in that unit does not change.
func parseTimeFlagAs(value, unitName string, toEpoch func(time.Time) int64) (int64, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, fmt.Errorf("empty time value")
	}
	if strings.EqualFold(v, "now") {
		return toEpoch(time.Now()), nil
	}
	// Relative duration. Allow leading +/-; map "d" suffix to hours.
	if v[0] == '+' || v[0] == '-' {
		if dur, err := parseRelativeDuration(v); err == nil {
			return toEpoch(time.Now().Add(dur)), nil
		}
		// Fall through to int parsing in case it's a negative epoch (rare).
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return toEpoch(t), nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: expected epoch %s, RFC3339, 'now', or relative duration like -1h, -7d", value, unitName)
	}
	return epochToUnit(n, toEpoch(time.Unix(1, 0))), nil
}

// epochToUnit converts an epoch value in seconds, milliseconds, microseconds
// or nanoseconds to a unit with perSec units in one second. The input unit is
// found from the magnitude: a present-day epoch has 10 digits in seconds, 13
// in ms, 16 in µs and 19 in ns.
//
// Commands in this CLI read different units, and the server returns no data,
// without an error, for a range in the wrong unit. A value that is already in
// the requested unit does not change for dates after 1973.
func epochToUnit(n, perSec int64) int64 {
	var from int64
	switch {
	case n >= 1e17:
		from = 1e9
	case n >= 1e14:
		from = 1e6
	case n >= 1e11:
		from = 1e3
	case n > 0:
		from = 1
	default:
		return n
	}
	if from >= perSec {
		return n / (from / perSec)
	}
	return n * (perSec / from)
}

// parseTimeFlagSeconds converts a time flag value to epoch seconds as float64.
// This is intentionally separate from parseTimeFlagAs because the Prometheus
// query API requires float64 epoch seconds (supporting sub-second precision),
// whereas the other time parsers return int64 in micro/milliseconds.
//
// Accepted forms:
//
//   - "now"            => current time
//   - "-1h", "-30m"    => relative durations
//   - "-7d"            => days; converted to hours
//   - RFC3339          => for example 2026-01-02T15:04:05Z
//   - number           => epoch seconds (int or float); an integer in ms,
//     µs or ns is converted by its magnitude
func parseTimeFlagSeconds(value string) (float64, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, fmt.Errorf("empty time value")
	}
	if strings.EqualFold(v, "now") {
		return float64(time.Now().Unix()), nil
	}
	// Relative duration. Allow leading +/-; map "d" suffix to hours.
	if v[0] == '+' || v[0] == '-' {
		if dur, err := parseRelativeDuration(v); err == nil {
			return float64(time.Now().Add(dur).Unix()), nil
		}
		// Fall through to float parsing in case it's a negative epoch (rare).
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return float64(t.UnixNano()) / 1e9, nil
	}
	// Numeric literal: epoch seconds (supports both int and float). An
	// integer in ms, µs or ns is converted to seconds by its magnitude.
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: expected epoch seconds, RFC3339, 'now', or relative duration like -1h, -7d", value)
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 1e11 {
		return float64(epochToUnit(n, 1e3)) / 1e3, nil
	}
	return f, nil
}

// parseRelativeDuration parses durations like "-1h", "-30m", "-7d", "-1d12h".
// "d" units are translated to hours (24h) before delegating to
// time.ParseDuration.
func parseRelativeDuration(v string) (time.Duration, error) {
	var b strings.Builder
	var num strings.Builder
	flushDigits := func() {
		b.WriteString(num.String())
		num.Reset()
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= '0' && c <= '9' {
			num.WriteByte(c)
			continue
		}
		if c == 'd' && num.Len() > 0 {
			n, err := strconv.Atoi(num.String())
			if err != nil {
				return 0, err
			}
			fmt.Fprintf(&b, "%dh", n*24)
			num.Reset()
			continue
		}
		flushDigits()
		b.WriteByte(c)
	}
	flushDigits()
	return time.ParseDuration(b.String())
}

// confirmAction prints prompt and waits for the user to type y/yes. If force
// is true the prompt is skipped and true is returned.
func confirmAction(prompt string, force bool) bool {
	if force {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
}

// hintNoData writes a hint to stderr when a read in a time range returns
// nothing. An empty table alone reads as proof that the data does not
// exist, when the data is often only outside the range. stdout stays
// clean, so -o json output is not changed.
func hintNoData(cmd *cobra.Command, what string, start, end time.Time) {
	fmt.Fprintf(cmd.ErrOrStderr(),
		"No %s between %s and %s. Only this range is read; widen it (for example --start -7d) before you decide that the data does not exist.\n",
		what, start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
}

// printResponseBody prints a response body that has no table form. JSON
// output is the body without change, and YAML output is converted from it.
// Other formats print JSON. Decoding the body into a generated type first
// drops the fields that the type does not know.
func printResponseBody(cmd *cobra.Command, format output.Format, body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("unexpected empty response")
	}
	if format == output.FormatYAML {
		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("parsing response: %w", err)
		}
		return output.Print(cmd.OutOrStdout(), format, parsed, nil)
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	buf.WriteByte('\n')
	_, err := cmd.OutOrStdout().Write(buf.Bytes())
	return err
}

// printBodyOrTable prints body without change for JSON and YAML output, and
// rows with columns for the table formats. Use it for resources that users
// get, edit and give back to update: a body decoded into a generated type
// and encoded again loses the fields that the type does not know, and the
// update then removes them on the server.
func printBodyOrTable(cmd *cobra.Command, body []byte, rows any, columns []output.Column) error {
	format := getOutputFormat(cmd)
	switch format {
	case output.FormatJSON, output.FormatYAML, "":
		return printResponseBody(cmd, format, body)
	}
	return output.Print(cmd.OutOrStdout(), format, rows, columns)
}
