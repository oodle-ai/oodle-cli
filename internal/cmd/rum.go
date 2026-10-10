package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	// rumDefaultLimit is the number of rows that a RUM read returns when
	// --limit is not set. The server uses the same default.
	rumDefaultLimit = 100

	// rumMessageWidth is the largest number of characters of a message or
	// URL in a table cell.
	rumMessageWidth = 80
)

// rumEventTypes are the event streams of a session, in the order that
// session-events reads them.
var rumEventTypes = []string{"views", "errors", "actions", "resources", "console"}

const rumWhereHelp = `Filter on an event field or a custom tag. Repeat the flag to add filters.
  key=value    equal         key!=value   not equal
  key=~regex   matches       key!~regex   does not match
Two key=value filters on the same key match either value. Two key=~regex
filters on the same key match either regex. Keys are fields such as
view_url_path, resource_url, error_type, or a custom tag name.`

const rumLong = `Read Real User Monitoring (RUM) data: browser sessions, the events in a
session, grouped issues and error events.

  sessions        List sessions, with view, error and action counts.
  session-events  Show the views, errors, actions, resources and console
                  messages of one session.
  issues          List errors grouped into issues, with occurrence counts.
  errors          List error events, with message and stack.

Each command reads only the time range in --start and --end (default: the
last hour). Use -o json to get every field.

Examples:
  # Sessions with errors in the last 6 hours
  oodle rum sessions --start -6h --has-errors

  # Everything that one session did
  oodle rum session-events 0f99e06c-93b4-41d2-88f6-2505ebea42d3 --start -24h

  # The top issues of the last day
  oodle rum issues --start -24h --limit 20

  # Error events with a message that contains "timeout"
  oodle rum errors --start -24h --message-contains timeout`

// newRUMCmd returns the `oodle rum` command tree.
func newRUMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rum",
		Short: "Read Real User Monitoring sessions, events, issues and errors",
		Long:  rumLong,
	}
	cmd.AddCommand(newRUMSessionsCmd())
	cmd.AddCommand(newRUMSessionEventsCmd())
	cmd.AddCommand(newRUMIssuesCmd())
	cmd.AddCommand(newRUMErrorsCmd())
	return cmd
}

// rumTagFilter is one entry of the tagFilters parameter.
type rumTagFilter struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

// parseRUMWhere converts --where values to the tagFilters parameter. It
// returns "" when there are no filters. Filters with the same key and an
// "=" or "=~" operator are merged into one filter, so that their values
// match as alternatives. The server rejects the full request for one bad
// filter, so the CLI checks each filter first.
func parseRUMWhere(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	var filters []rumTagFilter
	merge := map[string]int{}
	for _, v := range values {
		i := strings.IndexAny(v, "!=")
		if i <= 0 {
			return "", fmt.Errorf("--where %q: use key=value, key!=value, key=~regex or key!~regex", v)
		}
		key := strings.TrimSpace(v[:i])
		rest := v[i:]
		var op, value string
		switch {
		case strings.HasPrefix(rest, "!="):
			op, value = "neq", rest[2:]
		case strings.HasPrefix(rest, "!~"):
			op, value = "nre", rest[2:]
		case strings.HasPrefix(rest, "=~"):
			op, value = "re", rest[2:]
		case strings.HasPrefix(rest, "="):
			op, value = "eq", rest[1:]
		default:
			return "", fmt.Errorf("--where %q: use key=value, key!=value, key=~regex or key!~regex", v)
		}
		if key == "" || value == "" {
			return "", fmt.Errorf("--where %q: the key and the value must not be empty", v)
		}
		if op == "eq" || op == "re" {
			if idx, ok := merge[op+"\x00"+key]; ok {
				filters[idx].Values = append(filters[idx].Values, value)
				if op == "eq" {
					filters[idx].Operator = "oneof"
				}
				continue
			}
			merge[op+"\x00"+key] = len(filters)
		}
		filters = append(filters, rumTagFilter{Key: key, Operator: op, Values: []string{value}})
	}
	out, err := json.Marshal(filters)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// rumLimit checks --limit. The server caps a larger value without an error.
func rumLimit(limit int) error {
	if limit < 1 {
		return fmt.Errorf("--limit must be at least 1, got %d", limit)
	}
	return nil
}

// --- sessions ---

type rumSession struct {
	SessionID        string `json:"session_id"`
	UserID           string `json:"user_id"`
	UserEmail        string `json:"user_email"`
	Service          string `json:"service"`
	BrowserName      string `json:"browser_name"`
	DeviceType       string `json:"device_type"`
	GeoCountry       string `json:"geo_country"`
	Environment      string `json:"environment"`
	StartTime        string `json:"start_time"`
	EndTime          string `json:"end_time"`
	ViewCount        int64  `json:"view_count"`
	ErrorCount       int64  `json:"error_count"`
	ActionCount      int64  `json:"action_count"`
	FrustrationCount int64  `json:"frustration_count"`
}

type rumSessionRow struct {
	SessionID    string
	User         string
	Start        string
	Duration     string
	Views        int64
	Errors       int64
	Actions      int64
	Frustrations int64
	Browser      string
	Country      string
}

var rumSessionColumns = []output.Column{
	{Header: "SESSION ID", Field: "SessionID"},
	{Header: "USER", Field: "User"},
	{Header: "START (UTC)", Field: "Start"},
	{Header: "DURATION", Field: "Duration"},
	{Header: "VIEWS", Field: "Views"},
	{Header: "ERRORS", Field: "Errors"},
	{Header: "ACTIONS", Field: "Actions"},
	{Header: "FRUSTRATIONS", Field: "Frustrations"},
	{Header: "BROWSER", Field: "Browser"},
	{Header: "COUNTRY", Field: "Country"},
}

func rumSessionRows(sessions []rumSession) []rumSessionRow {
	rows := make([]rumSessionRow, 0, len(sessions))
	for _, s := range sessions {
		user := s.UserEmail
		if user == "" {
			user = s.UserID
		}
		var dur string
		start, err1 := time.Parse(time.RFC3339Nano, s.StartTime)
		end, err2 := time.Parse(time.RFC3339Nano, s.EndTime)
		if err1 == nil && err2 == nil && !end.Before(start) {
			dur = end.Sub(start).String()
		}
		rows = append(rows, rumSessionRow{
			SessionID:    s.SessionID,
			User:         user,
			Start:        utcCell(s.StartTime),
			Duration:     dur,
			Views:        s.ViewCount,
			Errors:       s.ErrorCount,
			Actions:      s.ActionCount,
			Frustrations: s.FrustrationCount,
			Browser:      s.BrowserName,
			Country:      s.GeoCountry,
		})
	}
	return rows
}

func newRUMSessionsCmd() *cobra.Command {
	var (
		limit                         int
		sessionID, userID, viewPath   string
		browser, device, country, env string
		errorSource, errorType        string
		resourceType, actionType      string
		where                         []string
		hasErrors, hasFrustration     bool
	)
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List RUM sessions",
		Long: `List RUM sessions in a time range.

The filters select events. A session is in the list when one or more of its
events match, and its times and counts cover only the matching events.

--has-errors and --has-frustration remove sessions from the list that the
server returns. They do not change the request, so a session can be outside
the first --limit sessions and not show. Increase --limit if the list is
short.

` + rumWhereHelp,
		Example: `  oodle rum sessions
  oodle rum sessions --start -6h --has-errors --limit 200
  oodle rum sessions --user-id alice@example.com --start -7d
  oodle rum sessions --where view_url_path=/checkout --where plan=pro -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.IntVar(&limit, "limit", rumDefaultLimit, "Maximum number of sessions to read")
	f.StringVar(&sessionID, "session-id", "", "Only this session")
	f.StringVar(&userID, "user-id", "", "Only sessions of this user ID")
	f.StringVar(&viewPath, "view-path", "", "Only events on this page path, such as /checkout")
	f.StringVar(&browser, "browser", "", "Only this browser name, such as Chrome")
	f.StringVar(&device, "device", "", "Only this device type, such as desktop or mobile")
	f.StringVar(&country, "country", "", "Only this country code, such as US")
	f.StringVar(&env, "env", "", "Only this environment")
	f.StringVar(&errorSource, "error-source", "", "Only errors from this source, such as source, network or console")
	f.StringVar(&errorType, "error-type", "", "Only errors of this type, such as TypeError")
	f.StringVar(&resourceType, "resource-type", "", "Only resources of this type, such as fetch or xhr")
	f.StringVar(&actionType, "action-type", "", "Only actions of this type, such as click")
	f.StringArrayVar(&where, "where", nil, "Event or tag filter: key=value, key!=value, key=~regex, key!~regex (repeatable)")
	f.BoolVar(&hasErrors, "has-errors", false, "Keep only sessions with one or more errors")
	f.BoolVar(&hasFrustration, "has-frustration", false, "Keep only sessions with one or more frustration signals")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := rumLimit(limit); err != nil {
			return err
		}
		tagFilters, err := parseRUMWhere(where)
		if err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("limit", strconv.Itoa(limit))
		setIfNotEmpty(params, "sessionId", sessionID)
		setIfNotEmpty(params, "userId", userID)
		setIfNotEmpty(params, "viewUrlPath", viewPath)
		setIfNotEmpty(params, "browserName", browser)
		setIfNotEmpty(params, "deviceType", device)
		setIfNotEmpty(params, "geoCountry", country)
		setIfNotEmpty(params, "environment", env)
		setIfNotEmpty(params, "errorSource", errorSource)
		setIfNotEmpty(params, "errorType", errorType)
		setIfNotEmpty(params, "resourceType", resourceType)
		setIfNotEmpty(params, "actionType", actionType)
		setIfNotEmpty(params, "tagFilters", tagFilters)
		body, err := instanceGet(cmd, "rum/sessions", params)
		if err != nil {
			return err
		}
		body, sessions, decodeErr := filterJSONArray(body, func(s rumSession) bool {
			return (!hasErrors || s.ErrorCount > 0) && (!hasFrustration || s.FrustrationCount > 0)
		})
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, rumSessionRows(sessions), rumSessionColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(sessions) == 0 {
			hintNoData(cmd, "sessions", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- session-events ---

// rumEvent holds the event fields that the tables show.
type rumEvent struct {
	Timestamp      string `json:"timestamp"`
	EventType      string `json:"event_type"`
	SessionID      string `json:"session_id"`
	ViewURL        string `json:"view_url"`
	ViewURLPath    string `json:"view_url_path"`
	ErrorSource    string `json:"error_source"`
	ErrorType      string `json:"error_type"`
	ErrorMessage   string `json:"error_message"`
	ResourceType   string `json:"resource_type"`
	ResourceURL    string `json:"resource_url"`
	ResourceMethod string `json:"resource_method"`
	ResourceStatus int    `json:"resource_status_code"`
	// The field name says ns, but the value is in milliseconds.
	ResourceDurationMs float64 `json:"resource_duration_ns"`
	ActionType         string  `json:"action_type"`
	ActionTargetName   string  `json:"action_target_name"`
	FrustrationType    string  `json:"frustration_type"`
	ConsoleLevel       string  `json:"console_level"`
	ConsoleMessage     string  `json:"console_message"`
}

// detail returns a one-line summary of the event for its type.
func (e rumEvent) detail(kind string) string {
	switch kind {
	case "views":
		if e.ViewURL != "" {
			return e.ViewURL
		}
		return e.ViewURLPath
	case "errors":
		return strings.TrimSpace(e.ErrorType + ": " + e.ErrorMessage)
	case "actions":
		d := strings.TrimSpace(e.ActionType + " " + strconv.Quote(e.ActionTargetName))
		if e.FrustrationType != "" {
			d += " [" + e.FrustrationType + "]"
		}
		return d
	case "resources":
		d := strings.TrimSpace(e.ResourceMethod + " " + e.ResourceURL)
		if e.ResourceStatus != 0 {
			d += " " + strconv.Itoa(e.ResourceStatus)
		}
		if e.ResourceDurationMs > 0 {
			d += " " + msDurationCell(e.ResourceDurationMs)
		}
		return d
	case "console":
		return strings.TrimSpace(e.ConsoleLevel + ": " + e.ConsoleMessage)
	}
	return ""
}

type rumTimelineRow struct {
	Time   string
	Type   string
	Page   string
	Detail string
	sortTS string
}

var rumTimelineColumns = []output.Column{
	{Header: "TIME (UTC)", Field: "Time"},
	{Header: "TYPE", Field: "Type"},
	{Header: "PAGE", Field: "Page"},
	{Header: "DETAIL", Field: "Detail"},
}

// rumTypeLabel is the singular name of an event type for the TYPE column.
var rumTypeLabel = map[string]string{
	"views": "view", "errors": "error", "actions": "action", "resources": "resource", "console": "console",
}

func parseRUMEventTypes(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return rumEventTypes, nil
	}
	want := map[string]bool{}
	for _, t := range strings.Split(value, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if !slices.Contains(rumEventTypes, t) {
			return nil, fmt.Errorf("--types: unknown type %q; use %s", t, strings.Join(rumEventTypes, ", "))
		}
		want[t] = true
	}
	var out []string
	for _, t := range rumEventTypes {
		if want[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--types must name one or more of %s", strings.Join(rumEventTypes, ", "))
	}
	return out, nil
}

func newRUMSessionEventsCmd() *cobra.Command {
	var (
		limit        int
		typesStr     string
		where        []string
		viewsGroupBy string
	)
	cmd := &cobra.Command{
		Use:   "session-events <session-id>",
		Short: "Show the events of one RUM session",
		Long: `Show the views, errors, actions, resources and console messages of one RUM
session.

The table is one timeline in time order. -o json gives an object with one
array for each event type.

The server returns at most --limit events of each type. A busy session can
have more resources than that. Use --types and --where to read only the
events that you need, for example the API calls to one route.

The session must start in the time range. Get the session start from
'oodle rum sessions', or widen --start.

` + rumWhereHelp,
		Example: `  oodle rum session-events 0f99e06c-93b4-41d2-88f6-2505ebea42d3
  oodle rum session-events 0f99e06c-93b4-41d2-88f6-2505ebea42d3 --start -24h --types errors,console
  oodle rum session-events 0f99e06c-93b4-41d2-88f6-2505ebea42d3 --types resources --where 'resource_url=~/api/orders'
  oodle rum session-events 0f99e06c-93b4-41d2-88f6-2505ebea42d3 --types views --views-group-by url -o json`,
		Args: exactArgs(1),
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.IntVar(&limit, "limit", rumDefaultLimit, "Maximum number of events of each type")
	f.StringVar(&typesStr, "types", "", "Comma-separated event types: views, errors, actions, resources, console (default: all)")
	f.StringArrayVar(&where, "where", nil, "Event or tag filter: key=value, key!=value, key=~regex, key!~regex (repeatable)")
	f.StringVar(&viewsGroupBy, "views-group-by", "", "How to group views: path (one row per page) or url (one row per full URL)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		sessionID := strings.TrimSpace(args[0])
		if sessionID == "" {
			return fmt.Errorf("session ID must not be empty")
		}
		if err := rumLimit(limit); err != nil {
			return err
		}
		types, err := parseRUMEventTypes(typesStr)
		if err != nil {
			return err
		}
		if viewsGroupBy != "" && viewsGroupBy != "path" && viewsGroupBy != "url" {
			return fmt.Errorf("--views-group-by must be path or url")
		}
		tagFilters, err := parseRUMWhere(where)
		if err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		streams := map[string]json.RawMessage{}
		var rows []rumTimelineRow
		for _, t := range types {
			params := msRangeParams(start, end)
			params.Set("sessionId", sessionID)
			params.Set("limit", strconv.Itoa(limit))
			setIfNotEmpty(params, "tagFilters", tagFilters)
			if t == "views" {
				setIfNotEmpty(params, "viewsGroupBy", viewsGroupBy)
			}
			body, err := instanceGet(cmd, "rum/"+t, params)
			if err != nil {
				return fmt.Errorf("reading %s: %w", t, err)
			}
			var events []rumEvent
			if err := json.Unmarshal(body, &events); err != nil {
				return fmt.Errorf("parsing %s: %w", t, err)
			}
			streams[t] = json.RawMessage(body)
			for _, e := range events {
				rows = append(rows, rumTimelineRow{
					Time:   utcCell(e.Timestamp),
					Type:   rumTypeLabel[t],
					Page:   e.ViewURLPath,
					Detail: shortCell(e.detail(t), 120),
					sortTS: e.Timestamp,
				})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].sortTS < rows[j].sortTS })
		out := map[string]any{"session_id": sessionID}
		for k, v := range streams {
			out[k] = v
		}
		body, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := printTraceQLResult(cmd, body, nil, func(format output.Format) error {
			return output.Print(cmd.OutOrStdout(), format, rows, rumTimelineColumns)
		}); err != nil {
			return err
		}
		if len(rows) == 0 {
			hintNoData(cmd, "events for session "+sessionID, time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- issues ---

type rumIssue struct {
	Fingerprint      string `json:"fingerprint"`
	ErrorType        string `json:"error_type"`
	ErrorMessage     string `json:"error_message"`
	ErrorSource      string `json:"error_source"`
	OccurrenceCount  int64  `json:"occurrence_count"`
	AffectedSessions int64  `json:"affected_sessions"`
	AffectedUsers    int64  `json:"affected_users"`
	FirstSeen        string `json:"first_seen"`
	LastSeen         string `json:"last_seen"`
	SamplePage       string `json:"sample_page"`
	SampleSessionID  string `json:"sample_session_id"`
}

type rumIssueRow struct {
	Type     string
	Source   string
	Message  string
	Count    int64
	Sessions int64
	Users    int64
	LastSeen string
	Page     string
	Sample   string
}

var rumIssueColumns = []output.Column{
	{Header: "TYPE", Field: "Type"},
	{Header: "SOURCE", Field: "Source"},
	{Header: "MESSAGE", Field: "Message"},
	{Header: "COUNT", Field: "Count"},
	{Header: "SESSIONS", Field: "Sessions"},
	{Header: "USERS", Field: "Users"},
	{Header: "LAST SEEN (UTC)", Field: "LastSeen"},
	{Header: "SAMPLE PAGE", Field: "Page"},
	{Header: "SAMPLE SESSION", Field: "Sample"},
}

func newRUMIssuesCmd() *cobra.Command {
	var (
		limit                  int
		errorType, errorSource string
		issueSource, search    string
		where                  []string
	)
	cmd := &cobra.Command{
		Use:   "issues",
		Short: "List RUM errors grouped into issues",
		Long: `List RUM errors grouped into issues. An issue is a group of errors with the
same fingerprint. Each row shows the number of occurrences and of affected
sessions and users in the time range.

Use the sample session with 'oodle rum session-events' to see what the user
did before the error. Use -o json to get the fingerprint and the stack.

` + rumWhereHelp,
		Example: `  oodle rum issues
  oodle rum issues --start -24h --limit 20
  oodle rum issues --source console --search "failed to fetch"
  oodle rum issues --error-type TypeError -o json`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.IntVar(&limit, "limit", rumDefaultLimit, "Maximum number of issues to read")
	f.StringVar(&errorType, "error-type", "", "Only issues of this error type, such as TypeError")
	f.StringVar(&errorSource, "error-source", "", "Only issues from this error source, such as source or network")
	f.StringVar(&issueSource, "source", "", "Only issues of this kind: error, console or frustration (default: all)")
	f.StringVar(&search, "search", "", "Only issues with this text in the message")
	f.StringArrayVar(&where, "where", nil, "Event or tag filter: key=value, key!=value, key=~regex, key!~regex (repeatable)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := rumLimit(limit); err != nil {
			return err
		}
		switch issueSource {
		case "", "error", "console", "frustration":
		default:
			return fmt.Errorf("--source must be error, console or frustration")
		}
		tagFilters, err := parseRUMWhere(where)
		if err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("limit", strconv.Itoa(limit))
		setIfNotEmpty(params, "errorType", errorType)
		setIfNotEmpty(params, "errorSource", errorSource)
		setIfNotEmpty(params, "issueSource", issueSource)
		setIfNotEmpty(params, "search", search)
		setIfNotEmpty(params, "tagFilters", tagFilters)
		body, err := instanceGet(cmd, "rum/issues", params)
		if err != nil {
			return err
		}
		var issues []rumIssue
		decodeErr := json.Unmarshal(body, &issues)
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			rows := make([]rumIssueRow, 0, len(issues))
			for _, i := range issues {
				rows = append(rows, rumIssueRow{
					Type:     i.ErrorType,
					Source:   i.ErrorSource,
					Message:  shortCell(i.ErrorMessage, rumMessageWidth),
					Count:    i.OccurrenceCount,
					Sessions: i.AffectedSessions,
					Users:    i.AffectedUsers,
					LastSeen: utcCell(i.LastSeen),
					Page:     i.SamplePage,
					Sample:   i.SampleSessionID,
				})
			}
			return output.Print(cmd.OutOrStdout(), format, rows, rumIssueColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(issues) == 0 {
			hintNoData(cmd, "issues", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}

// --- errors ---

type rumErrorRow struct {
	Time    string
	Source  string
	Type    string
	Message string
	Page    string
	Session string
}

var rumErrorColumns = []output.Column{
	{Header: "TIME (UTC)", Field: "Time"},
	{Header: "SOURCE", Field: "Source"},
	{Header: "TYPE", Field: "Type"},
	{Header: "MESSAGE", Field: "Message"},
	{Header: "PAGE", Field: "Page"},
	{Header: "SESSION ID", Field: "Session"},
}

func newRUMErrorsCmd() *cobra.Command {
	var (
		limit                  int
		sessionID, userID      string
		errorType, errorSource string
		contains               string
		where                  []string
	)
	cmd := &cobra.Command{
		Use:   "errors",
		Short: "List RUM error events",
		Long: `List RUM error events, one row for each error. Use -o json to get the stack
and the user, page and browser of each error.

--message-contains removes errors from the list that the server returns. It
does not change the request, so a match can be outside the first --limit
errors and not show. Increase --limit, or use 'oodle rum issues --search',
which the server applies.

` + rumWhereHelp,
		Example: `  oodle rum errors
  oodle rum errors --start -24h --error-type TypeError
  oodle rum errors --session-id 0f99e06c-93b4-41d2-88f6-2505ebea42d3 -o json
  oodle rum errors --message-contains timeout --limit 200`,
		Args: cobra.NoArgs,
	}
	parseRange := addRangeFlags(cmd, defaultStartOffset, parseTimeFlagMs)
	f := cmd.Flags()
	f.IntVar(&limit, "limit", rumDefaultLimit, "Maximum number of errors to read")
	f.StringVar(&sessionID, "session-id", "", "Only errors of this session")
	f.StringVar(&userID, "user-id", "", "Only errors of this user ID")
	f.StringVar(&errorType, "error-type", "", "Only errors of this type, such as TypeError")
	f.StringVar(&errorSource, "error-source", "", "Only errors from this source, such as source, network or console")
	f.StringVar(&contains, "message-contains", "", "Keep only errors with this text in the message (not case-sensitive)")
	f.StringArrayVar(&where, "where", nil, "Event or tag filter: key=value, key!=value, key=~regex, key!~regex (repeatable)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := rumLimit(limit); err != nil {
			return err
		}
		tagFilters, err := parseRUMWhere(where)
		if err != nil {
			return err
		}
		start, end, err := parseRange()
		if err != nil {
			return err
		}
		params := msRangeParams(start, end)
		params.Set("limit", strconv.Itoa(limit))
		setIfNotEmpty(params, "sessionId", sessionID)
		setIfNotEmpty(params, "userId", userID)
		setIfNotEmpty(params, "errorType", errorType)
		setIfNotEmpty(params, "errorSource", errorSource)
		setIfNotEmpty(params, "tagFilters", tagFilters)
		body, err := instanceGet(cmd, "rum/errors", params)
		if err != nil {
			return err
		}
		needle := strings.ToLower(strings.TrimSpace(contains))
		body, events, decodeErr := filterJSONArray(body, func(e rumEvent) bool {
			return needle == "" || strings.Contains(strings.ToLower(e.ErrorMessage), needle)
		})
		if err := printTraceQLResult(cmd, body, decodeErr, func(format output.Format) error {
			rows := make([]rumErrorRow, 0, len(events))
			for _, e := range events {
				rows = append(rows, rumErrorRow{
					Time:    utcCell(e.Timestamp),
					Source:  e.ErrorSource,
					Type:    e.ErrorType,
					Message: shortCell(e.ErrorMessage, rumMessageWidth),
					Page:    e.ViewURLPath,
					Session: e.SessionID,
				})
			}
			return output.Print(cmd.OutOrStdout(), format, rows, rumErrorColumns)
		}); err != nil {
			return err
		}
		if decodeErr == nil && len(events) == 0 {
			hintNoData(cmd, "error events", time.UnixMilli(start), time.UnixMilli(end))
		}
		return nil
	}
	return cmd
}
