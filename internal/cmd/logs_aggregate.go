package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// logsAggDefaultSize is the number of values that a terms aggregation
	// returns when --size is not set.
	logsAggDefaultSize = 10

	// logsAggMaxSize is the largest --size that the CLI accepts. A larger
	// terms aggregation makes the server hold too many buckets in memory.
	logsAggMaxSize = 1000

	// logsAggMaxBuckets is the largest number of histogram buckets that the
	// CLI asks for. Without a bound, a long range with a short interval makes
	// the server compute very many buckets.
	logsAggMaxBuckets = 2000

	// Names of the aggregations in the request body. The response uses the
	// same names, so the parser finds the buckets by these names.
	logsAggTermsName = "values"
	logsAggHistName  = "over_time"
)

// logsIntervalRe matches a fixed histogram interval such as 30s, 5m, 1h or
// 1d. The server rejects calendar units and other forms with an error that
// does not name the flag.
var logsIntervalRe = regexp.MustCompile(`^([1-9][0-9]*)(ms|s|m|h|d)$`)

// logsAggRange holds the parsed --start and --end values.
type logsAggRange struct {
	startMs, endMs int64
}

// buildLogsAggBody returns the NDJSON body for one search on index that
// counts no hits and computes aggs. The time range and the optional Lucene
// query go in a bool filter, so they limit the logs without scoring.
func buildLogsAggBody(index, query string, r logsAggRange, aggs map[string]any) ([]byte, error) {
	filter := []any{}
	if strings.TrimSpace(query) != "" {
		filter = append(filter, map[string]any{
			"query_string": map[string]any{"query": query},
		})
	}
	filter = append(filter, map[string]any{
		"range": map[string]any{
			"timestamp": map[string]any{
				"gte":    r.startMs,
				"lte":    r.endMs,
				"format": "epoch_millis",
			},
		},
	})
	search := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": filter}},
		"aggs":  aggs,
	}
	header, err := json.Marshal(map[string]any{"index": index})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(search)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(header)
	buf.WriteByte('\n')
	buf.Write(body)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// termsAgg returns a terms aggregation on field with the largest counts first.
func termsAgg(field string, size int) map[string]any {
	return map[string]any{
		"terms": map[string]any{
			"field": field,
			"size":  size,
			"order": map[string]any{"_count": "desc"},
		},
	}
}

// logsAggBucket is one bucket of a terms or date_histogram aggregation.
type logsAggBucket struct {
	Key         json.RawMessage `json:"key"`
	KeyAsString string          `json:"key_as_string"`
	DocCount    int64           `json:"doc_count"`
	Values      *logsAggTerms   `json:"values"`
}

// logsAggTerms is the result of a terms aggregation.
type logsAggTerms struct {
	Buckets          []logsAggBucket `json:"buckets"`
	SumOtherDocCount int64           `json:"sum_other_doc_count"`
}

// logsAggResult is the part of one search result that the aggregation
// commands read.
type logsAggResult struct {
	Total    int64
	Values   *logsAggTerms
	OverTime *logsAggTerms
}

// runLogsAgg sends body and returns the first search result. A search error
// in the response body becomes a command error: the server sends it with a
// success status, and without this check the command prints nothing and
// exits with success. field is the --count-by or field-values field, or
// empty.
func runLogsAgg(cmd *cobra.Command, body []byte, index, field string) (logsAggResult, error) {
	c := getClient(cmd)
	params := &client.QueryLogsParams{XOODLEINSTANCE: getInstance(cmd)}
	respBody, err := readRawBody(c.Inner.QueryLogsWithBody(cmd.Context(), params, "application/x-ndjson", bytes.NewReader(body)))
	if err != nil {
		return logsAggResult{}, err
	}
	res, err := parseLogsAggResponse(respBody)
	if err != nil && field != "" && strings.HasPrefix(err.Error(), "search failed") {
		// The server does not say which part of the query failed. A field
		// that the index does not have is the usual cause, so name it.
		return res, fmt.Errorf("%w. Check that %s is a field of index %s; field names are case sensitive", err, field, index)
	}
	return res, err
}

// parseLogsAggResponse decodes a multi-search response with one search.
func parseLogsAggResponse(body []byte) (logsAggResult, error) {
	var resp struct {
		Responses []struct {
			Error json.RawMessage `json:"error"`
			Hits  *struct {
				Total json.RawMessage `json:"total"`
			} `json:"hits"`
			Aggregations struct {
				Values   *logsAggTerms `json:"values"`
				OverTime *logsAggTerms `json:"over_time"`
			} `json:"aggregations"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return logsAggResult{}, fmt.Errorf("parsing response: %w", err)
	}
	if len(resp.Responses) == 0 {
		return logsAggResult{}, fmt.Errorf("unexpected empty response")
	}
	r := resp.Responses[0]
	if e := bytes.TrimSpace(r.Error); len(e) > 0 && !bytes.Equal(e, []byte("null")) {
		return logsAggResult{}, fmt.Errorf("search failed: %s", searchErrorMessage(e))
	}
	out := logsAggResult{Values: r.Aggregations.Values, OverTime: r.Aggregations.OverTime}
	if r.Hits != nil {
		out.Total = parseHitsTotal(r.Hits.Total)
	}
	return out, nil
}

// searchErrorMessage returns the message of a search error, or the error
// JSON when it has no message.
func searchErrorMessage(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Reason != "" {
			return e.Reason
		}
	}
	return string(raw)
}

// parseHitsTotal reads hits.total as a number or as {"value": n}. Search
// back ends send both forms.
func parseHitsTotal(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var obj struct {
		Value int64 `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Value
	}
	return 0
}

// bucketValue returns a bucket key as text. A string key is unquoted. A
// numeric key uses key_as_string when the server sends it.
func bucketValue(b logsAggBucket) string {
	var s string
	if json.Unmarshal(b.Key, &s) == nil {
		return s
	}
	if b.KeyAsString != "" {
		return b.KeyAsString
	}
	return string(b.Key)
}

// bucketTime returns a date_histogram bucket key as an RFC3339 UTC time.
func bucketTime(b logsAggBucket) string {
	var ms float64
	if json.Unmarshal(b.Key, &ms) == nil {
		return time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
	}
	return b.KeyAsString
}

// logsValueRow is one value of a field and the number of logs with it.
type logsValueRow struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// logsTimeRow is one histogram bucket.
type logsTimeRow struct {
	Time  string `json:"time"`
	Count int64  `json:"count"`
}

// logsTimeValueRow is one value of a field in one histogram bucket.
type logsTimeValueRow struct {
	Time  string `json:"time"`
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// valueRows converts terms buckets to rows.
func valueRows(t *logsAggTerms) []logsValueRow {
	rows := []logsValueRow{}
	if t == nil {
		return rows
	}
	for _, b := range t.Buckets {
		rows = append(rows, logsValueRow{Value: bucketValue(b), Count: b.DocCount})
	}
	return rows
}

// valueColumns names the value column after the field, so a table shows
// which field the values come from.
func valueColumns(field string) []output.Column {
	return []output.Column{
		{Header: strings.ToUpper(field), Field: "Value"},
		{Header: "COUNT", Field: "Count"},
	}
}

// hintOtherValues writes a note when the terms aggregation left out values.
// Without it, the top values read as the full list.
func hintOtherValues(cmd *cobra.Command, t *logsAggTerms, size int) {
	if t == nil || t.SumOtherDocCount == 0 {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"%d more logs have values that are not shown. Only the top %d values are shown; increase --size to see more.\n",
		t.SumOtherDocCount, size)
}

// hintNoValues writes the empty-result hint for a terms aggregation. When
// logs matched but no log has the field, the field name is the likely
// problem, not the range.
func hintNoValues(cmd *cobra.Command, field string, res logsAggResult, r logsAggRange) {
	if res.Total > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%d logs matched, but none has a value for %s. Check the field name; field names are case sensitive.\n",
			res.Total, field)
		return
	}
	hintNoData(cmd, "logs", time.UnixMilli(r.startMs), time.UnixMilli(r.endMs))
}

// validateLogsAggSize checks --size, so a bad value fails with a message
// that names the flag.
func validateLogsAggSize(size int) error {
	if size < 1 || size > logsAggMaxSize {
		return fmt.Errorf("--size must be between 1 and %d", logsAggMaxSize)
	}
	return nil
}

// addLogsAggFlags adds the flags that field-values and aggregate share.
// The returned function gives the time range.
func addLogsAggFlags(cmd *cobra.Command, index, query *string) func() (logsAggRange, error) {
	cmd.Flags().StringVarP(index, "index", "i", "", "Log index pattern to read (run 'oodle logs index-patterns' to list them)")
	cmd.Flags().StringVarP(query, "query", "q", "", "Lucene query that selects the logs to count, for example 'level:error AND cluster:prod'")
	_ = cmd.MarkFlagRequired("index")
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	return func() (logsAggRange, error) {
		start, end, err := parseRange()
		return logsAggRange{startMs: start, endMs: end}, err
	}
}

// newLogsFieldValuesCmd returns the `oodle logs field-values` subcommand.
func newLogsFieldValuesCmd() *cobra.Command {
	var index, query string
	var size int
	var parseRange func() (logsAggRange, error)
	cmd := &cobra.Command{
		Use:   "field-values <field>",
		Short: "List the most frequent values of a log field",
		Long: `List the most frequent values of a log field, with the number of logs
that have each value. The largest counts come first.

Use it to find the values to put in a query, for example the container names
or log levels in an index. Use --query to count only the logs that match a
Lucene query.

Run 'oodle logs index-patterns' to list the index names.`,
		Example: `  oodle logs field-values container_name --index my_logs
  oodle logs field-values level --index my_logs --start -24h --size 20
  oodle logs field-values pod_name -i my_logs -q 'container_name:api' -o json`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			field := strings.TrimSpace(args[0])
			if field == "" {
				return fmt.Errorf("field name must not be empty")
			}
			if err := validateLogsAggSize(size); err != nil {
				return err
			}
			r, err := parseRange()
			if err != nil {
				return err
			}
			body, err := buildLogsAggBody(index, query, r, map[string]any{
				logsAggTermsName: termsAgg(field, size),
			})
			if err != nil {
				return fmt.Errorf("building query: %w", err)
			}
			res, err := runLogsAgg(cmd, body, index, field)
			if err != nil {
				return err
			}
			rows := valueRows(res.Values)
			if err := output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), rows, valueColumns(field)); err != nil {
				return err
			}
			if len(rows) == 0 {
				hintNoValues(cmd, field, res, r)
				return nil
			}
			hintOtherValues(cmd, res.Values, size)
			return nil
		},
	}
	parseRange = addLogsAggFlags(cmd, &index, &query)
	cmd.Flags().IntVar(&size, "size", logsAggDefaultSize, fmt.Sprintf("Number of values to return (1-%d)", logsAggMaxSize))
	return cmd
}

// newLogsAggregateCmd returns the `oodle logs aggregate` subcommand.
func newLogsAggregateCmd() *cobra.Command {
	var index, query, countBy, interval string
	var size int
	var parseRange func() (logsAggRange, error)
	cmd := &cobra.Command{
		Use:   "aggregate",
		Short: "Count logs by field value, over time, or both",
		Long: `Count logs without reading them. Set one or both of these flags:

  --count-by <field>     Count the logs for each value of a field. The values
                         with the largest counts come first (see --size).
  --histogram <interval> Count the logs in each time bucket. The interval is
                         a number and a unit: ms, s, m, h or d (for example
                         30s, 5m, 1h).

With both flags, each time bucket shows the top values of the field in that
bucket. Use --query to count only the logs that match a Lucene query.

For other aggregations, write the query body yourself and use
'oodle logs query'.`,
		Example: `  oodle logs aggregate --index my_logs --count-by container_name
  oodle logs aggregate --index my_logs --histogram 5m --start -6h
  oodle logs aggregate -i my_logs --histogram 1h --count-by level -q 'cluster:prod' --size 3 --start -24h`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			countBy = strings.TrimSpace(countBy)
			interval = strings.TrimSpace(interval)
			if countBy == "" && interval == "" {
				return fmt.Errorf("set --count-by, --histogram, or both")
			}
			if err := validateLogsAggSize(size); err != nil {
				return err
			}
			r, err := parseRange()
			if err != nil {
				return err
			}

			aggs := map[string]any{}
			switch {
			case interval == "":
				aggs[logsAggTermsName] = termsAgg(countBy, size)
			default:
				if err := validateHistogramInterval(interval, r); err != nil {
					return err
				}
				hist := map[string]any{
					"date_histogram": map[string]any{
						"field":          "timestamp",
						"fixed_interval": interval,
					},
				}
				if countBy != "" {
					hist["aggs"] = map[string]any{logsAggTermsName: termsAgg(countBy, size)}
				}
				aggs[logsAggHistName] = hist
			}

			body, err := buildLogsAggBody(index, query, r, aggs)
			if err != nil {
				return fmt.Errorf("building query: %w", err)
			}
			res, err := runLogsAgg(cmd, body, index, countBy)
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			format := getOutputFormat(cmd)
			switch {
			case interval == "":
				rows := valueRows(res.Values)
				if err := output.Print(w, format, rows, valueColumns(countBy)); err != nil {
					return err
				}
				if len(rows) == 0 {
					hintNoValues(cmd, countBy, res, r)
					return nil
				}
				hintOtherValues(cmd, res.Values, size)
				return nil
			case countBy == "":
				rows := []logsTimeRow{}
				if res.OverTime != nil {
					for _, b := range res.OverTime.Buckets {
						rows = append(rows, logsTimeRow{Time: bucketTime(b), Count: b.DocCount})
					}
				}
				if err := output.Print(w, format, rows, []output.Column{
					{Header: "TIME", Field: "Time"},
					{Header: "COUNT", Field: "Count"},
				}); err != nil {
					return err
				}
			default:
				rows := []logsTimeValueRow{}
				if res.OverTime != nil {
					for _, b := range res.OverTime.Buckets {
						t := bucketTime(b)
						if b.Values == nil {
							continue
						}
						for _, v := range b.Values.Buckets {
							rows = append(rows, logsTimeValueRow{Time: t, Value: bucketValue(v), Count: v.DocCount})
						}
					}
				}
				if err := output.Print(w, format, rows, []output.Column{
					{Header: "TIME", Field: "Time"},
					{Header: strings.ToUpper(countBy), Field: "Value"},
					{Header: "COUNT", Field: "Count"},
				}); err != nil {
					return err
				}
				if len(rows) == 0 && res.Total > 0 {
					hintNoValues(cmd, countBy, res, r)
					return nil
				}
			}
			if res.Total == 0 {
				hintNoData(cmd, "logs", time.UnixMilli(r.startMs), time.UnixMilli(r.endMs))
			}
			return nil
		},
	}
	parseRange = addLogsAggFlags(cmd, &index, &query)
	cmd.Flags().StringVar(&countBy, "count-by", "", "Field whose values to count")
	cmd.Flags().StringVar(&interval, "histogram", "", "Count logs in time buckets of this size (for example 30s, 5m, 1h, 1d)")
	cmd.Flags().IntVar(&size, "size", logsAggDefaultSize, fmt.Sprintf("Number of --count-by values to return, in each time bucket with --histogram (1-%d)", logsAggMaxSize))
	return cmd
}

// validateHistogramInterval checks the --histogram value and the number of
// buckets that it gives for the range.
func validateHistogramInterval(interval string, r logsAggRange) error {
	m := logsIntervalRe.FindStringSubmatch(interval)
	if m == nil {
		return fmt.Errorf("--histogram %q: use a number and a unit (ms, s, m, h or d), for example 5m", interval)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return fmt.Errorf("--histogram %q: %w", interval, err)
	}
	unit := map[string]int64{"ms": 1, "s": 1e3, "m": 60e3, "h": 3600e3, "d": 86400e3}[m[2]]
	stepMs := n * unit
	if buckets := (r.endMs - r.startMs) / stepMs; buckets > logsAggMaxBuckets {
		return fmt.Errorf("--histogram %s gives %d buckets for this range; the limit is %d. Use a larger interval or a shorter range",
			interval, buckets, logsAggMaxBuckets)
	}
	return nil
}
