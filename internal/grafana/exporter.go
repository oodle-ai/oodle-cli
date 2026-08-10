package grafana

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"


)

const (
	OodleDataDir             = "oodle_data"
	DashboardsDir            = "dashboards"
	FoldersDir               = "folders"
	DatasourcesDir           = "datasources"
	AlertingDir              = "alerting"
	AlertRulesDir            = "alert_rules"
	ContactPointsDir         = "contact_points"
	NotificationPoliciesDir  = "notification_policies"
	MuteTimingsDir           = "mute_timings"
	NotificationTemplatesDir = "notification_templates"
)

// Exporter handles exporting data from a Grafana instance
type Exporter struct {
	client  *Client
	config  ExportConfig
	baseDir string
}

// NewExporter creates a new Grafana exporter
func NewExporter(config ExportConfig) *Exporter {
	return &Exporter{
		client: NewClient(config.GrafanaURL, config.GrafanaToken),
		config: config,
	}
}

// Export exports all data from Grafana and returns the path to the tar.gz file
func (e *Exporter) Export(ctx context.Context) (*ExportResult, error) {
	return e.ExportWithProgress(ctx, nil)
}

// ExportWithProgress exports all data from Grafana with progress callbacks
func (e *Exporter) ExportWithProgress(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) (*ExportResult, error) {
	// Create a unique temp directory for this export
	tempDir, err := os.MkdirTemp("", "grafana-export-*")
	if err != nil {
		return nil, wrapErr(err, "failed to create temp directory")
	}
	e.baseDir = filepath.Join(tempDir, OodleDataDir)

	if err := e.setupDirectories(); err != nil {
		return nil, wrapErr(err, "failed to setup directories")
	}

	if err := e.client.CheckHealth(ctx); err != nil {
		return nil, wrapErr(err, "failed to connect to Grafana")
	}

	if err := e.exportDashboardsWithProgress(ctx, progressCallback); err != nil {
		return nil, wrapErr(err, "failed to export dashboards")
	}

	visitedFolders := make(map[string]struct{})
	if err := e.exportFolders(ctx, "", visitedFolders); err != nil {
		return nil, wrapErr(err, "failed to export folders")
	}

	if err := e.exportDatasourcesWithProgress(ctx, progressCallback); err != nil {
		return nil, wrapErr(err, "failed to export datasources")
	}

	if err := e.exportAlertingWithProgress(ctx, progressCallback); err != nil {
		return nil, wrapErr(err, "failed to export alerting")
	}

	tarFilePath := filepath.Join(tempDir, "oodle_data.tar.gz")
	if err := CompressDirectory(e.baseDir, tarFilePath); err != nil {
		return nil, wrapErr(err, "failed to compress directory")
	}

	return &ExportResult{
		TarFilePath: tarFilePath,
		ExportDir:   tempDir,
	}, nil
}

// Cleanup removes the temporary export directory
func (e *Exporter) Cleanup(result *ExportResult) {
	if result != nil && result.ExportDir != "" {
		os.RemoveAll(result.ExportDir)
	}
}

// Preview does a full export from Grafana and returns both the export result
// (tar file) and preview metadata. The export file can be reused for import.
// Supports progress callback for streaming real-time updates.
func (e *Exporter) Preview(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) (*PreviewResult, *ExportResult, error) {
	// Do full export which downloads all data from Grafana
	exportResult, err := e.ExportWithProgress(ctx, progressCallback)
	if err != nil {
		return nil, nil, err
	}

	// Build preview result from the exported data
	// Read the metadata from exported files
	previewResult, err := e.buildPreviewFromExport(exportResult.ExportDir)
	if err != nil {
		e.Cleanup(exportResult)
		return nil, nil, wrapErr(err, "failed to build preview from export")
	}

	return previewResult, exportResult, nil
}

// buildPreviewFromExport reads exported files to build preview metadata
func (e *Exporter) buildPreviewFromExport(exportDir string) (*PreviewResult, error) {
	result := &PreviewResult{
		Dashboards:  []Dashboard{},
		DataSources: []Datasource{},
		AlertRules:  []AlertRule{},
	}

	// Files are in exportDir/oodle_data/
	baseDir := filepath.Join(exportDir, OodleDataDir)

	// Read dashboards
	dashboardsDir := filepath.Join(baseDir, DashboardsDir)
	dashFiles, _ := os.ReadDir(dashboardsDir)
	for _, f := range dashFiles {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dashboardsDir, f.Name()))
		if err != nil {
			continue
		}
		var dashWrapper struct {
			Dashboard struct {
				UID   string   `json:"uid"`
				Title string   `json:"title"`
				Tags  []string `json:"tags"`
			} `json:"dashboard"`
			Meta struct {
				FolderUID   string `json:"folderUid"`
				FolderTitle string `json:"folderTitle"`
				URL         string `json:"url"`
				IsStarred   bool   `json:"isStarred"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(data, &dashWrapper); err != nil {
			continue
		}
		result.Dashboards = append(result.Dashboards, Dashboard{
			UID:         dashWrapper.Dashboard.UID,
			Title:       dashWrapper.Dashboard.Title,
			Tags:        dashWrapper.Dashboard.Tags,
			FolderUID:   dashWrapper.Meta.FolderUID,
			FolderTitle: dashWrapper.Meta.FolderTitle,
			URL:         dashWrapper.Meta.URL,
			IsStarred:   dashWrapper.Meta.IsStarred,
		})
	}

	// Read datasources
	datasourcesDir := filepath.Join(baseDir, DatasourcesDir)
	dsFiles, _ := os.ReadDir(datasourcesDir)
	for _, f := range dsFiles {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(datasourcesDir, f.Name()))
		if err != nil {
			continue
		}
		var ds Datasource
		if err := json.Unmarshal(data, &ds); err != nil {
			continue
		}
		result.DataSources = append(result.DataSources, ds)
	}

	// Read alert rules (each file is a single AlertRule, not a group)
	alertRulesDir := filepath.Join(baseDir, AlertingDir, AlertRulesDir)
	alertFiles, _ := os.ReadDir(alertRulesDir)
	for _, f := range alertFiles {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(alertRulesDir, f.Name()))
		if err != nil {
			continue
		}
		var rule AlertRule
		if err := json.Unmarshal(data, &rule); err != nil {
			continue
		}
		result.AlertRules = append(result.AlertRules, rule)
	}

	return result, nil
}

func (e *Exporter) setupDirectories() error {
	dirs := []string{
		filepath.Join(e.baseDir, DashboardsDir),
		filepath.Join(e.baseDir, FoldersDir),
		filepath.Join(e.baseDir, DatasourcesDir),
		filepath.Join(e.baseDir, AlertingDir, AlertRulesDir),
		filepath.Join(e.baseDir, AlertingDir, ContactPointsDir),
		filepath.Join(e.baseDir, AlertingDir, NotificationPoliciesDir),
		filepath.Join(e.baseDir, AlertingDir, MuteTimingsDir),
		filepath.Join(e.baseDir, AlertingDir, NotificationTemplatesDir),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return wrapErrf(err, "failed to create directory %s", dir)
		}
	}
	return nil
}

func (e *Exporter) exportDashboardsWithProgress(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) error {
	dashboards, err := e.client.GetDashboards(ctx)
	if err != nil {
		return err
	}

	// Build set of selected UIDs if specified
	selectedUIDs := make(map[string]struct{})
	hasSelection := len(e.config.SelectedDashboardUIDs) > 0
	for _, uid := range e.config.SelectedDashboardUIDs {
		selectedUIDs[uid] = struct{}{}
	}

	var filteredDashboards []Dashboard
	for _, dash := range dashboards {
		// Apply tag filter first
		if !HasMatchingTags(dash.Tags, e.config.IncludeTags) {
			continue
		}
		// Then apply selection filter if specified
		if hasSelection {
			if _, ok := selectedUIDs[dash.UID]; !ok {
				continue
			}
		}
		filteredDashboards = append(filteredDashboards, dash)
	}

	total := len(filteredDashboards)
	for i, dash := range filteredDashboards {
		if progressCallback != nil {
			progressCallback(ExportProgress{
				CurrentStep: "dashboards",
				Total:       total,
				Completed:   i,
			})
		}

		dashRespBody, err := e.client.GetDashboardByUID(ctx, dash.UID)
		if err != nil {
			return err
		}

		filePath := filepath.Join(e.baseDir, DashboardsDir, dash.UID+".json")
		if err := os.WriteFile(filePath, dashRespBody, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write dashboard file %s",
				dash.UID,
			)
		}
	}

	if progressCallback != nil && total > 0 {
		progressCallback(ExportProgress{
			CurrentStep: "dashboards",
			Total:       total,
			Completed:   total,
		})
	}

	return nil
}

func (e *Exporter) exportFolders(
	ctx context.Context,
	parentUID string,
	visitedFolders map[string]struct{},
) error {
	folders, err := e.client.GetFolders(ctx, parentUID)
	if err != nil {
		return err
	}

	for _, folder := range folders {
		if _, ok := visitedFolders[folder.UID]; ok {
			continue
		}

		visitedFolders[folder.UID] = struct{}{}
		folderRespBody, err := e.client.GetFolderByUID(ctx, folder.UID)
		if err != nil {
			return err
		}

		filePath := filepath.Join(e.baseDir, FoldersDir, folder.UID+".json")
		if err := os.WriteFile(filePath, folderRespBody, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write folder file %s",
				folder.UID,
			)
		}

		// Export subfolders recursively
		if err := e.exportFolders(ctx, folder.UID, visitedFolders); err != nil {
			return err
		}
	}

	return nil
}

func (e *Exporter) exportDatasourcesWithProgress(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) error {
	datasources, err := e.client.GetDatasources(ctx)
	if err != nil {
		return err
	}

	// Build set of selected UIDs if specified
	selectedUIDs := make(map[string]struct{})
	hasSelection := len(e.config.SelectedDataSourceUIDs) > 0
	for _, uid := range e.config.SelectedDataSourceUIDs {
		selectedUIDs[uid] = struct{}{}
	}

	var filteredDatasources []Datasource
	for _, ds := range datasources {
		if hasSelection {
			if _, ok := selectedUIDs[ds.UID]; !ok {
				continue
			}
		}
		filteredDatasources = append(filteredDatasources, ds)
	}

	total := len(filteredDatasources)
	for i, ds := range filteredDatasources {
		if progressCallback != nil {
			progressCallback(ExportProgress{
				CurrentStep: "data_sources",
				Total:       total,
				Completed:   i,
			})
		}

		dsRespBody, err := e.client.GetDatasourceByUID(ctx, ds.UID)
		if err != nil {
			return err
		}

		filePath := filepath.Join(e.baseDir, DatasourcesDir, ds.UID+".json")
		if err := os.WriteFile(filePath, dsRespBody, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write datasource file %s",
				ds.UID,
			)
		}
	}

	if progressCallback != nil && total > 0 {
		progressCallback(ExportProgress{
			CurrentStep: "data_sources",
			Total:       total,
			Completed:   total,
		})
	}

	return nil
}

func (e *Exporter) exportAlertingWithProgress(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) error {
	if err := e.exportAlertRulesWithProgress(ctx, progressCallback); err != nil {
		return err
	}
	if err := e.exportContactPoints(ctx); err != nil {
		return err
	}
	if err := e.exportNotificationPolicies(ctx); err != nil {
		return err
	}
	if err := e.exportMuteTimings(ctx); err != nil {
		return err
	}
	if err := e.exportNotificationTemplates(ctx); err != nil {
		return err
	}
	return nil
}

func (e *Exporter) exportAlertRulesWithProgress(
	ctx context.Context,
	progressCallback ExportProgressCallback,
) error {
	alertRules, err := e.client.GetAlertRules(ctx)
	if err != nil {
		return err
	}

	// Build set of selected UIDs if specified
	selectedUIDs := make(map[string]struct{})
	hasSelection := len(e.config.SelectedAlertRuleUIDs) > 0
	for _, uid := range e.config.SelectedAlertRuleUIDs {
		selectedUIDs[uid] = struct{}{}
	}

	var filteredAlertRules []AlertRule
	for _, rule := range alertRules {
		if hasSelection {
			if _, ok := selectedUIDs[rule.UID]; !ok {
				continue
			}
		}
		filteredAlertRules = append(filteredAlertRules, rule)
	}

	total := len(filteredAlertRules)
	for i, rule := range filteredAlertRules {
		if progressCallback != nil {
			progressCallback(ExportProgress{
				CurrentStep: "alerts",
				Total:       total,
				Completed:   i,
			})
		}

		ruleRespBody, err := e.client.GetAlertRuleByUID(ctx, rule.UID)
		if err != nil {
			return err
		}

		filePath := filepath.Join(
			e.baseDir,
			AlertingDir,
			AlertRulesDir,
			rule.UID+".json",
		)
		if err := os.WriteFile(filePath, ruleRespBody, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write alert rule file %s",
				rule.UID,
			)
		}
	}

	if progressCallback != nil && total > 0 {
		progressCallback(ExportProgress{
			CurrentStep: "alerts",
			Total:       total,
			Completed:   total,
		})
	}

	return nil
}

func (e *Exporter) exportContactPoints(ctx context.Context) error {
	contactPoints, err := e.client.GetContactPoints(ctx)
	if err != nil {
		return err
	}

	for _, cp := range contactPoints {
		fileName := cp.UID
		if fileName == "" {
			fileName = strings.ReplaceAll(cp.Name, "/", "_")
		}

		cpData, err := json.MarshalIndent(cp, "", "  ")
		if err != nil {
			return wrapErrf(
				err,
				"failed to marshal contact point %s",
				cp.Name,
			)
		}

		filePath := filepath.Join(
			e.baseDir,
			AlertingDir,
			ContactPointsDir,
			fileName+".json",
		)
		if err := os.WriteFile(filePath, cpData, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write contact point file %s",
				cp.Name,
			)
		}
	}

	return nil
}

func (e *Exporter) exportNotificationPolicies(ctx context.Context) error {
	respBody, err := e.client.GetNotificationPolicies(ctx)
	if err != nil {
		return err
	}

	filePath := filepath.Join(
		e.baseDir,
		AlertingDir,
		NotificationPoliciesDir,
		"policies.json",
	)
	if err := os.WriteFile(filePath, respBody, 0644); err != nil {
		return wrapErr(err, "failed to write notification policies file")
	}

	return nil
}

func (e *Exporter) exportMuteTimings(ctx context.Context) error {
	muteTimings, err := e.client.GetMuteTimings(ctx)
	if err != nil {
		return err
	}

	for _, mt := range muteTimings {
		fileName := strings.ReplaceAll(mt.Name, "/", "_")
		mtData, err := json.MarshalIndent(mt, "", "  ")
		if err != nil {
			return wrapErrf(
				err,
				"failed to marshal mute timing %s",
				mt.Name,
			)
		}

		filePath := filepath.Join(
			e.baseDir,
			AlertingDir,
			MuteTimingsDir,
			fileName+".json",
		)
		if err := os.WriteFile(filePath, mtData, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write mute timing file %s",
				mt.Name,
			)
		}
	}

	return nil
}

func (e *Exporter) exportNotificationTemplates(ctx context.Context) error {
	templates, err := e.client.GetNotificationTemplates(ctx)
	if err != nil {
		return err
	}

	for _, tmpl := range templates {
		fileName := strings.ReplaceAll(tmpl.Name, "/", "_")
		tmplData, err := json.MarshalIndent(tmpl, "", "  ")
		if err != nil {
			return wrapErrf(
				err,
				"failed to marshal notification template %s",
				tmpl.Name,
			)
		}

		filePath := filepath.Join(
			e.baseDir,
			AlertingDir,
			NotificationTemplatesDir,
			fileName+".json",
		)
		if err := os.WriteFile(filePath, tmplData, 0644); err != nil {
			return wrapErrf(
				err,
				"failed to write notification template file %s",
				tmpl.Name,
			)
		}
	}

	return nil
}

// SetupExportDirectories sets up the export directory structure at the given
// base path
func SetupExportDirectories(baseDir string) error {
	dirs := []string{
		filepath.Join(baseDir, DashboardsDir),
		filepath.Join(baseDir, FoldersDir),
		filepath.Join(baseDir, DatasourcesDir),
		filepath.Join(baseDir, AlertingDir, AlertRulesDir),
		filepath.Join(baseDir, AlertingDir, ContactPointsDir),
		filepath.Join(baseDir, AlertingDir, NotificationPoliciesDir),
		filepath.Join(baseDir, AlertingDir, MuteTimingsDir),
		filepath.Join(baseDir, AlertingDir, NotificationTemplatesDir),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return wrapErrf(err, "failed to create directory %s", dir)
		}
	}
	return nil
}
