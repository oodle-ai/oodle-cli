package cmd

import (
	"bufio"
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

// Default time-range values applied when --start or --end are omitted from
// exploration commands (e.g. metrics names/labels, logs query).
const (
	defaultStartOffset = "-1h"
	defaultEndValue    = "now"
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
		if err := yaml.Unmarshal(data, v); err != nil {
			return fmt.Errorf("parsing YAML from %s: %w", path, err)
		}
	default:
		// YAML is a superset of JSON; try yaml first, fall back to json.
		if err := yaml.Unmarshal(data, v); err != nil {
			if jerr := json.Unmarshal(data, v); jerr != nil {
				return fmt.Errorf("parsing %s (tried YAML and JSON): %w", path, err)
			}
		}
	}
	return nil
}

// parseTimeFlag converts a time flag value to epoch microseconds. Accepted
// forms:
//
//   - "now"            => current time
//   - "-1h", "-30m"    => relative durations (Go's time.ParseDuration)
//   - "-7d"            => days; converted to hours
//   - integer          => epoch microseconds, returned as-is
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

// parseTimeFlagAs is the shared core for parseTimeFlag and parseTimeFlagMs.
// unitName is the human-readable unit used in error messages ("microseconds",
// "milliseconds"). toEpoch converts a time.Time to the desired epoch unit
// (e.g. time.Time.UnixMicro). Integer literals are passed through verbatim
// and are assumed to be in the requested unit already.
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
	// Integer literal: assumed to already be in the requested unit.
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: expected epoch %s, 'now', or relative duration like -1h, -7d", value, unitName)
	}
	return n, nil
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
//   - number           => epoch seconds, returned as-is (supports both int and float)
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
	// Numeric literal: epoch seconds (supports both int and float).
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: expected epoch seconds, 'now', or relative duration like -1h, -7d", value)
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
