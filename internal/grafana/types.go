package grafana

// ExportConfig contains configuration for exporting from Grafana
type ExportConfig struct {
	GrafanaURL   string
	GrafanaToken string
	IncludeTags  []string
	// Selected UIDs for selective migration
	// For CLI: if empty, exports all items
	// For wizard: frontend should ensure at least one item is selected
	SelectedDashboardUIDs  []string
	SelectedDataSourceUIDs []string
	SelectedAlertRuleUIDs  []string
}

// ExportResult contains the result of an export operation
type ExportResult struct {
	TarFilePath string
	ExportDir   string
}

// ExportProgress represents progress during export
type ExportProgress struct {
	// CurrentStep: "dashboards", "data_sources", "alerts"
	CurrentStep string
	Total       int
	Completed   int
}

// ExportProgressCallback is called during export to report progress
type ExportProgressCallback func(progress ExportProgress)

// NoOpExportProgressCallback is a no-op callback for functions that don't need
// progress updates.
var NoOpExportProgressCallback ExportProgressCallback = func(ExportProgress) {}

// PreviewResult contains the data available for migration without exporting
type PreviewResult struct {
	Dashboards  []Dashboard
	DataSources []Datasource
	AlertRules  []AlertRule
}

// Dashboard represents a Grafana dashboard in search results
type Dashboard struct {
	UID           string   `json:"uid"`
	Title         string   `json:"title"`
	Tags          []string `json:"tags"`
	FolderUID     string   `json:"folderUid,omitempty"`
	FolderTitle   string   `json:"folderTitle,omitempty"`
	URL           string   `json:"url,omitempty"`
	IsStarred     bool     `json:"isStarred,omitempty"`
	SortMeta      int      `json:"sortMeta,omitempty"`
	Type          string   `json:"type,omitempty"`
	DataSourceIDs []string `json:"dataSourceIds,omitempty"`
}

// Folder represents a Grafana folder
type Folder struct {
	UID   string `json:"uid"`
	Title string `json:"title,omitempty"`
}

// Datasource represents a Grafana datasource
type Datasource struct {
	UID        string `json:"uid"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	URL        string `json:"url,omitempty"`
	IsDefault  bool   `json:"isDefault,omitempty"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
	Access     string `json:"access,omitempty"`
	Database   string `json:"database,omitempty"`
	BasicAuth  bool   `json:"basicAuth,omitempty"`
	TypeLogoID string `json:"typeLogoUrl,omitempty"`
	Supported  bool   `json:"supported"`
}

// AlertRule represents a Grafana alert rule
type AlertRule struct {
	UID          string            `json:"uid"`
	Title        string            `json:"title"`
	FolderUID    string            `json:"folderUID"`
	RuleGroup    string            `json:"ruleGroup"`
	OrgID        int64             `json:"orgId"`
	Condition    string            `json:"condition"`
	NoDataState  string            `json:"noDataState"`
	ExecErrState string            `json:"execErrState"`
	For          string            `json:"for"`
	Annotations  map[string]string `json:"annotations"`
	Labels       map[string]string `json:"labels"`
	IsPaused     bool              `json:"isPaused"`
}

// AlertRuleGroup represents a group of alert rules
type AlertRuleGroup struct {
	FolderUID string      `json:"folderUid"`
	Title     string      `json:"title"`
	Interval  int64       `json:"interval"`
	Rules     []AlertRule `json:"rules"`
}

// ContactPoint represents a Grafana contact point
type ContactPoint struct {
	UID                   string                 `json:"uid"`
	Name                  string                 `json:"name"`
	Type                  string                 `json:"type"`
	Settings              map[string]interface{} `json:"settings"`
	DisableResolveMessage bool                   `json:"disableResolveMessage"`
	Provenance            string                 `json:"provenance,omitempty"`
}

// NotificationPolicy represents the notification policy tree
type NotificationPolicy struct {
	Receiver          string                `json:"receiver"`
	GroupBy           []string              `json:"group_by,omitempty"`
	ObjectMatchers    [][]string            `json:"object_matchers,omitempty"`
	MuteTimeIntervals []string              `json:"mute_time_intervals,omitempty"`
	Continue          bool                  `json:"continue,omitempty"`
	GroupWait         string                `json:"group_wait,omitempty"`
	GroupInterval     string                `json:"group_interval,omitempty"`
	RepeatInterval    string                `json:"repeat_interval,omitempty"`
	Routes            []*NotificationPolicy `json:"routes,omitempty"`
	Provenance        string                `json:"provenance,omitempty"`
}

// TimeRange represents a time range in a mute timing
type TimeRange struct {
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
}

// TimeInterval represents intervals of time for mute timings
type TimeInterval struct {
	Times       []TimeRange `json:"times,omitempty"`
	Weekdays    []string    `json:"weekdays,omitempty"`
	DaysOfMonth []string    `json:"days_of_month,omitempty"`
	Months      []string    `json:"months,omitempty"`
	Years       []string    `json:"years,omitempty"`
	Location    string      `json:"location,omitempty"`
}

// MuteTiming represents a Grafana mute timing
type MuteTiming struct {
	Name          string         `json:"name"`
	TimeIntervals []TimeInterval `json:"time_intervals"`
	Provenance    string         `json:"provenance,omitempty"`
}

// NotificationTemplate represents a Grafana notification template
type NotificationTemplate struct {
	Name       string `json:"name"`
	Template   string `json:"template"`
	Provenance string `json:"provenance,omitempty"`
}
