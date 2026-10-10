package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// dbmDefaultSamples is the number of samples that 'dbm samples' reads
	// when --limit is not set. The server uses the same default.
	dbmDefaultSamples = 100

	// dbmDefaultActivity is the number of samples that 'dbm activity' reads
	// when --limit is not set.
	dbmDefaultActivity = 50

	// dbmMaxSamples is the largest --limit that the server accepts. The CLI
	// checks it, because the server cuts a larger value without an error.
	dbmMaxSamples = 1000

	// dbmQueryWidth is the largest number of characters of a SQL statement
	// in a table cell.
	dbmQueryWidth = 80
)

const dbmLong = `Read database monitoring (DBM) data: the monitored database hosts, query
samples, blocking between sessions, and explain plans.

  hosts     List monitored database hosts with query counts and durations.
  samples   List query samples: the statements that ran, with duration,
            state and wait event.
  activity  Show recent samples, and the blocking chains of one query
            signature.
  explain   Show the stored explain plans of one query signature.

A query signature identifies one normalized statement. Get it from the
SIGNATURE column of 'oodle dbm samples'.

Each command reads only the time range in --start and --end (default: the
last hour). Use -o json to get every field.

Examples:
  oodle dbm hosts --start -24h
  oodle dbm samples --host db-1 --min-duration 2s
  oodle dbm activity --query-signature 8d2c1f0e9a7b6c5d
  oodle dbm explain 8d2c1f0e9a7b6c5d --start -24h`

// newDBMCmd returns the `oodle dbm` command tree.
func newDBMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dbm",
		Aliases: []string{"databases"},
		Short:   "Read database monitoring hosts, query samples and explain plans",
		Long:    dbmLong,
	}
	cmd.AddCommand(newDBMHostsCmd())
	cmd.AddCommand(newDBMSamplesCmd())
	cmd.AddCommand(newDBMActivityCmd())
	cmd.AddCommand(newDBMExplainCmd())
	return cmd
}

// msDurationCell formats a number of milliseconds as a duration.
func msDurationCell(ms float64) string {
	if ms <= 0 {
		return "0s"
	}
	return time.Duration(ms * float64(time.Millisecond)).Round(time.Microsecond).String()
}

// secDurationCell formats a number of seconds as a duration.
func secDurationCell(sec float64) string {
	if sec <= 0 {
		return "0s"
	}
	return time.Duration(sec * float64(time.Second)).Round(time.Millisecond).String()
}

// --- hosts ---

type dbmHost struct {
	Host                 string   `json:"host"`
	DBInstanceIdentifier string   `json:"dbInstanceIdentifier"`
	DatabaseType         string   `json:"databaseType"`
	Version              string   `json:"version"`
	Count                int64    `json:"count"`
	AvgDuration          float64  `json:"avgDuration"`
	MaxDuration          float64  `json:"maxDuration"`
	AvgCPUUtilization    *float64 `json:"avgCPUUtilization"`
}

type dbmHostRow struct {
	Host     string
	Type     string
	Version  string
	Instance string
	Calls    int64
	Avg      string
	Max      string
	CPU      string
}

var dbmHostColumns = []output.Column{
	{Header: "HOST", Field: "Host"},
	{Header: "TYPE", Field: "Type"},
	{Header: "VERSION", Field: "Version"},
	{Header: "DB INSTANCE", Field: "Instance"},
	{Header: "CALLS", Field: "Calls"},
	{Header: "AVG DURATION", Field: "Avg"},
	{Header: "MAX DURATION", Field: "Max"},
	{Header: "AVG CPU %", Field: "CPU"},
}

func newDBMHostsCmd() *cobra.Command {
	var dbType, host, dbInstance, version string
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "List monitored database hosts",
		Long: `List the database hosts that sent monitoring data in the time range. Each
row shows the number of query calls, the average and maximum duration, and
the average CPU use when the cloud provider sends it.`,
		Example: `  oodle dbm hosts
  oodle dbm hosts --start -24h --database-type postgres
  oodle dbm hosts --host db-1 -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addMsRangeFlags(cmd)
	f := cmd.Flags()
	f.StringVar(&dbType, "database-type", "", "Only this database type, such as postgres")
	f.StringVar(&host, "host", "", "Only this host")
	f.StringVar(&dbInstance, "db-instance", "", "Only this cloud database instance identifier")
	f.StringVar(&version, "version", "", "Only this database version")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		setIfNotEmpty(params, "database_type", dbType)
		setIfNotEmpty(params, "host", host)
		setIfNotEmpty(params, "db_instance_identifier", dbInstance)
		setIfNotEmpty(params, "version", version)
		body, err := instanceGet(cmd, "dbm/database-hosts", params)
		if err != nil {
			return err
		}
		var hosts []dbmHost
		decodeErr := json.Unmarshal(body, &hosts)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			rows := make([]dbmHostRow, 0, len(hosts))
			for _, h := range hosts {
				row := dbmHostRow{
					Host:     h.Host,
					Type:     h.DatabaseType,
					Version:  h.Version,
					Instance: h.DBInstanceIdentifier,
					Calls:    h.Count,
					Avg:      msDurationCell(h.AvgDuration),
					Max:      msDurationCell(h.MaxDuration),
				}
				if h.AvgCPUUtilization != nil {
					row.CPU = strconv.FormatFloat(*h.AvgCPUUtilization, 'f', 1, 64)
				}
				rows = append(rows, row)
			}
			return output.Print(cmd.OutOrStdout(), format, rows, dbmHostColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(hosts) == 0 {
			hintNoData(cmd, "database hosts", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- samples ---

type dbmSample struct {
	OodleGUID       string  `json:"oodle_guid"`
	Timestamp       int64   `json:"timestamp"`
	QuerySignature  string  `json:"query_signature"`
	NormalizedQuery string  `json:"normalized_query"`
	Duration        float64 `json:"duration"`
	Host            string  `json:"host"`
	Database        string  `json:"database"`
	User            string  `json:"user"`
	State           string  `json:"state"`
	WaitEvent       *string `json:"wait_event"`
	WaitEventType   *string `json:"wait_event_type"`
}

type dbmSampleRow struct {
	Time      string
	Host      string
	Database  string
	User      string
	Duration  string
	State     string
	Wait      string
	Signature string
	Query     string
}

var dbmSampleColumns = []output.Column{
	{Header: "TIME (UTC)", Field: "Time"},
	{Header: "HOST", Field: "Host"},
	{Header: "DATABASE", Field: "Database"},
	{Header: "USER", Field: "User"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "STATE", Field: "State"},
	{Header: "WAIT", Field: "Wait"},
	{Header: "SIGNATURE", Field: "Signature"},
	{Header: "QUERY", Field: "Query"},
}

func dbmSampleRows(samples []dbmSample) []dbmSampleRow {
	rows := make([]dbmSampleRow, 0, len(samples))
	for _, s := range samples {
		var wait string
		if s.WaitEventType != nil && *s.WaitEventType != "" {
			wait = *s.WaitEventType
		}
		if s.WaitEvent != nil && *s.WaitEvent != "" {
			if wait != "" {
				wait += ":"
			}
			wait += *s.WaitEvent
		}
		rows = append(rows, dbmSampleRow{
			Time:      msCell(s.Timestamp),
			Host:      s.Host,
			Database:  s.Database,
			User:      s.User,
			Duration:  secDurationCell(s.Duration),
			State:     s.State,
			Wait:      wait,
			Signature: s.QuerySignature,
			Query:     shortCell(s.NormalizedQuery, dbmQueryWidth),
		})
	}
	return rows
}

// parseDurationSeconds parses a duration flag such as 500ms or 2s, or a
// number of seconds, and returns seconds. Sample durations are in seconds.
func parseDurationSeconds(value string) (float64, error) {
	v := strings.TrimSpace(value)
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: use a duration such as 500ms or 2s, or a number of seconds", value)
	}
	return d.Seconds(), nil
}

// dbmSamplesLimit checks --limit for the samples route.
func dbmSamplesLimit(limit int) error {
	if limit < 1 || limit > dbmMaxSamples {
		return fmt.Errorf("--limit must be between 1 and %d, got %d", dbmMaxSamples, limit)
	}
	return nil
}

func newDBMSamplesCmd() *cobra.Command {
	var (
		limit                          int
		sortBy, sortOrder              string
		signature, database, host      string
		user, guid                     string
		tables, commands               []string
		minDurationStr, maxDurationStr string
	)
	cmd := &cobra.Command{
		Use:   "samples",
		Short: "List database query samples",
		Long: `List database query samples. A sample is one statement that a database
session ran at the time of a snapshot, with its duration, state and wait
event.

The DURATION column is the time that the statement had run at the snapshot.
Use the SIGNATURE value with --query-signature, 'oodle dbm activity' or
'oodle dbm explain'.`,
		Example: `  oodle dbm samples
  oodle dbm samples --host db-1 --database orders --min-duration 2s
  oodle dbm samples --query-signature 8d2c1f0e9a7b6c5d --start -24h
  oodle dbm samples --table orders --command SELECT --sort-by duration -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addMsRangeFlags(cmd)
	f := cmd.Flags()
	f.IntVar(&limit, "limit", dbmDefaultSamples, "Maximum number of samples (1 to 1000)")
	f.StringVar(&sortBy, "sort-by", "", "Field to sort by, such as timestamp or duration (default: timestamp)")
	f.StringVar(&sortOrder, "sort-order", "", "Sort order: asc or desc (default: desc)")
	f.StringVar(&signature, "query-signature", "", "Only samples of this query signature")
	f.StringVar(&database, "database", "", "Only samples in this database")
	f.StringVar(&host, "host", "", "Only samples on this host")
	f.StringVar(&user, "user", "", "Only samples of this database user")
	f.StringVar(&guid, "guid", "", "Only the sample with this ID")
	f.StringArrayVar(&tables, "table", nil, "Only samples that use this table (repeatable)")
	f.StringArrayVar(&commands, "command", nil, "Only samples of this SQL command, such as SELECT (repeatable)")
	f.StringVar(&minDurationStr, "min-duration", "", "Only samples that ran this long or longer, such as 500ms or 2s")
	f.StringVar(&maxDurationStr, "max-duration", "", "Only samples that ran this long or shorter, such as 500ms or 2s")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := dbmSamplesLimit(limit); err != nil {
			return err
		}
		if sortOrder != "" && sortOrder != "asc" && sortOrder != "desc" {
			return fmt.Errorf("--sort-order must be asc or desc")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("limit", strconv.Itoa(limit))
		setIfNotEmpty(params, "sortBy", sortBy)
		setIfNotEmpty(params, "sortOrder", sortOrder)
		setIfNotEmpty(params, "querySignature", signature)
		setIfNotEmpty(params, "database", database)
		setIfNotEmpty(params, "host", host)
		setIfNotEmpty(params, "user", user)
		setIfNotEmpty(params, "oodleGuid", guid)
		for _, t := range tables {
			params.Add("tables", t)
		}
		for _, c := range commands {
			params.Add("commands", c)
		}
		for flag, v := range map[string]string{"minDuration": minDurationStr, "maxDuration": maxDurationStr} {
			if v == "" {
				continue
			}
			sec, err := parseDurationSeconds(v)
			if err != nil {
				name := "--min-duration"
				if flag == "maxDuration" {
					name = "--max-duration"
				}
				return fmt.Errorf("%s: %w", name, err)
			}
			params.Set(flag, strconv.FormatFloat(sec, 'f', -1, 64))
		}
		body, err := instanceGet(cmd, "dbm/samples", params)
		if err != nil {
			return err
		}
		var samples []dbmSample
		decodeErr := json.Unmarshal(body, &samples)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, dbmSampleRows(samples), dbmSampleColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(samples) == 0 {
			hintNoData(cmd, "query samples", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- activity ---

type dbmBlockingItem struct {
	PID         int64   `json:"pid"`
	Host        string  `json:"host"`
	Statement   string  `json:"statement"`
	WaitEvent   *string `json:"wait_event"`
	DurationMs  int64   `json:"duration_ms"`
	Signature   string  `json:"query_signature"`
	Database    string  `json:"database_name"`
	User        string  `json:"user_name"`
	Application string  `json:"application_name"`
}

// decodeDBMBlocking reads a blocking list. The waiting route names the
// fields blocker_*, and the blocking route names them blocked_*. The prefix
// is removed, so that one table shape shows both lists.
func decodeDBMBlocking(body []byte, prefix string) ([]dbmBlockingItem, error) {
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	items := make([]dbmBlockingItem, 0, len(raw))
	for _, r := range raw {
		m := make(map[string]json.RawMessage, len(r))
		for k, v := range r {
			m[strings.TrimPrefix(k, prefix)] = v
		}
		// The duration field has a different name on each route.
		for _, k := range []string{"blocked_duration_ms", "blocking_duration_ms"} {
			if v, ok := r[k]; ok {
				m["duration_ms"] = v
			}
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		var it dbmBlockingItem
		if err := json.Unmarshal(b, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

type dbmBlockingRow struct {
	PID       int64
	Host      string
	Database  string
	User      string
	Duration  string
	Wait      string
	Signature string
	Query     string
}

var dbmBlockingColumns = []output.Column{
	{Header: "PID", Field: "PID"},
	{Header: "HOST", Field: "Host"},
	{Header: "DATABASE", Field: "Database"},
	{Header: "USER", Field: "User"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "WAIT", Field: "Wait"},
	{Header: "SIGNATURE", Field: "Signature"},
	{Header: "QUERY", Field: "Query"},
}

func dbmBlockingRows(items []dbmBlockingItem) []dbmBlockingRow {
	rows := make([]dbmBlockingRow, 0, len(items))
	for _, it := range items {
		var wait string
		if it.WaitEvent != nil {
			wait = *it.WaitEvent
		}
		rows = append(rows, dbmBlockingRow{
			PID:       it.PID,
			Host:      it.Host,
			Database:  it.Database,
			User:      it.User,
			Duration:  msDurationCell(float64(it.DurationMs)),
			Wait:      wait,
			Signature: it.Signature,
			Query:     shortCell(it.Statement, dbmQueryWidth),
		})
	}
	return rows
}

func newDBMActivityCmd() *cobra.Command {
	var (
		limit     int
		signature string
	)
	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent database activity and blocking",
		Long: `Show the recent query samples, with their state and wait event.

With --query-signature, the command also shows the blocking chains of that
statement:
  Blocked by  the sessions that held a lock that the statement waited for
  Blocking    the sessions that waited for a lock that the statement held

-o json gives an object with the arrays recent_samples, blocking_waiting
and blocking_blocking.`,
		Example: `  oodle dbm activity
  oodle dbm activity --limit 20
  oodle dbm activity --query-signature 8d2c1f0e9a7b6c5d --start -6h`,
		Args: cobra.NoArgs,
	}
	parseRange := addMsRangeFlags(cmd)
	cmd.Flags().IntVar(&limit, "limit", dbmDefaultActivity, "Maximum number of recent samples (1 to 1000)")
	cmd.Flags().StringVar(&signature, "query-signature", "", "Also show the blocking chains of this query signature")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := dbmSamplesLimit(limit); err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("limit", strconv.Itoa(limit))
		samplesBody, err := instanceGet(cmd, "dbm/samples", params)
		if err != nil {
			return fmt.Errorf("reading samples: %w", err)
		}
		var samples []dbmSample
		if err := json.Unmarshal(samplesBody, &samples); err != nil {
			return fmt.Errorf("parsing samples: %w", err)
		}
		out := map[string]json.RawMessage{"recent_samples": samplesBody}
		var waiting, blocking []dbmBlockingItem
		if signature != "" {
			bp := msRangeParams(start, end)
			bp.Set("querySignature", signature)
			for _, r := range []struct {
				route, key, prefix string
				dst                *[]dbmBlockingItem
			}{
				{"dbm/queries/blocking-activity/waiting", "blocking_waiting", "blocker_", &waiting},
				{"dbm/queries/blocking-activity/blocking", "blocking_blocking", "blocked_", &blocking},
			} {
				body, err := instanceGet(cmd, r.route, bp)
				if err != nil {
					return fmt.Errorf("reading %s: %w", r.key, err)
				}
				items, err := decodeDBMBlocking(body, r.prefix)
				if err != nil {
					return fmt.Errorf("parsing %s: %w", r.key, err)
				}
				*r.dst = items
				out[r.key] = body
			}
		}
		body, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := printTraceQLResult(cmd, body, nil, func(format output.Format) error {
			w := cmd.OutOrStdout()
			if err := output.Print(w, format, dbmSampleRows(samples), dbmSampleColumns); err != nil {
				return err
			}
			if signature == "" {
				return nil
			}
			for _, sec := range []struct {
				title string
				items []dbmBlockingItem
			}{
				{"Blocked by (sessions that held a lock that this statement waited for):", waiting},
				{"Blocking (sessions that waited for a lock that this statement held):", blocking},
			} {
				fmt.Fprintf(w, "\n%s\n", sec.title)
				if len(sec.items) == 0 {
					fmt.Fprintln(w, "  none")
					continue
				}
				if err := output.Print(w, format, dbmBlockingRows(sec.items), dbmBlockingColumns); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(samples) == 0 && len(waiting) == 0 && len(blocking) == 0 {
			hintNoData(cmd, "database activity", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- explain ---

type dbmExplainPlans struct {
	QuerySignature string   `json:"query_signature"`
	ExplainPlans   []string `json:"explain_plans"`
}

// printExplainPlans writes each plan as text. A plan in JSON is indented,
// because the database stores it on one line.
func printExplainPlans(w io.Writer, sets []dbmExplainPlans) error {
	n := 0
	for _, s := range sets {
		for _, p := range s.ExplainPlans {
			n++
			fmt.Fprintf(w, "--- Plan %d (signature %s) ---\n", n, s.QuerySignature)
			var buf bytes.Buffer
			if json.Indent(&buf, []byte(p), "", "  ") == nil {
				p = buf.String()
			}
			if _, err := fmt.Fprintln(w, strings.TrimRight(p, "\n")); err != nil {
				return err
			}
		}
	}
	return nil
}

func newDBMExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain <query-signature>",
		Short: "Show the explain plans of a query signature",
		Long: `Show the explain plans that were stored for a query signature in the time
range. Get the signature from the SIGNATURE column of 'oodle dbm samples'.

The table output prints each plan as text. -o json gives the server
response.`,
		Example: `  oodle dbm explain 8d2c1f0e9a7b6c5d
  oodle dbm explain 8d2c1f0e9a7b6c5d --start -24h -o json`,
		Args: exactArgs(1),
	}
	parseRange := addMsRangeFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		signature := strings.TrimSpace(args[0])
		if signature == "" {
			return fmt.Errorf("query signature must not be empty")
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("querySignature", signature)
		body, err := instanceGet(cmd, "dbm/queries/explain-plans", params)
		if err != nil {
			return err
		}
		var sets []dbmExplainPlans
		decodeErr := json.Unmarshal(body, &sets)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			if format == output.FormatTable {
				return printExplainPlans(cmd.OutOrStdout(), sets)
			}
			type row struct {
				Signature string
				Plan      string
			}
			var rows []row
			for _, s := range sets {
				for _, p := range s.ExplainPlans {
					rows = append(rows, row{Signature: s.QuerySignature, Plan: p})
				}
			}
			return output.Print(cmd.OutOrStdout(), format, rows, []output.Column{
				{Header: "SIGNATURE", Field: "Signature"},
				{Header: "PLAN", Field: "Plan"},
			})
		}); err != nil {
			return err
		}
		plans := 0
		for _, s := range sets {
			plans += len(s.ExplainPlans)
		}
		if decodeErr == nil && plans == 0 {
			hintNoData(cmd, "explain plans for "+signature, time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}
