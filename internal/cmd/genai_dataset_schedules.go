package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var scheduleColumns = []output.Column{
	{Header: "DATASET", Field: "DatasetId"},
	{Header: "ENABLED", Field: "Enabled"},
	{Header: "MODE", Field: "Mode"},
	{Header: "NEXT RUN", Field: "NextRunAt"},
	{Header: "LAST RUN", Field: "LastRunAt"},
	{Header: "ERROR", Field: "LastError"},
}

// Schedule modes, matching the server's spelling.
const (
	scheduleModeCalendar = "calendar"
	scheduleModeInterval = "interval"
)

// newGenAIDatasetScheduleCmd returns
// `oodle genai datasets schedule`.
//
// A dataset holds at most one schedule, so this is a singleton
// with no ids of its own: set replaces whatever is there.
func newGenAIDatasetScheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedule",
		Aliases: []string{"schedules"},
		Short:   "Manage a dataset's recurring experiment",
		Long: `Run a dataset's experiment on a schedule.

A dataset is only measured against a prompt or model change
when someone remembers to start a run, so a regression is found
whenever the next person looks. A schedule runs the experiment
on its own and holds at most one definition per dataset.

Two shapes, chosen by --mode:

  calendar   at times of day in a named timezone, optionally
             narrowed to weekdays or days of the month. Use it
             when a run has to land at a time someone cares
             about; the times follow daylight saving rather
             than drifting twice a year.
  interval   every N minutes, hours or days, set with --every.
             Use it when the cadence matters but the wall-clock
             time does not.

What a firing runs is an experiment config, the same one
` + "`oodle genai experiments run`" + ` takes — give it with the same
flags, or with --file. A firing starts shortly after it is due
rather than exactly on the minute, and a schedule that falls
behind runs once instead of replaying every firing it missed.`,
	}
	cmd.AddCommand(newGenAIDatasetScheduleGetCmd())
	cmd.AddCommand(newGenAIDatasetScheduleSetCmd())
	cmd.AddCommand(newGenAIDatasetScheduleDeleteCmd())
	return cmd
}

func newGenAIDatasetScheduleGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <dataset-name>",
		Short: "Get a dataset's experiment schedule",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiDatasetScheduleWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON200, scheduleColumns)
		},
	}
}

func newGenAIDatasetScheduleSetCmd() *cobra.Command {
	var (
		file        string
		enabled     bool
		mode        string
		every       string
		times       []string
		timezone    string
		weekdays    []string
		daysOfMonth []string
		cfg         experimentConfigFlags
	)
	cmd := &cobra.Command{
		Use:   "set <dataset-name>",
		Short: "Set a dataset's experiment schedule",
		Long: `Create or replace a dataset's experiment schedule.

The whole schedule is replaced, so give every field you want
kept — this is a PUT, not a patch.

  # Every six hours.
  oodle genai datasets schedule set support-eval \
    --every 6h --dataset-id <id> --connection-id <id> \
    --model gpt-4o --prompt-name support-reply

  # Weekday mornings, Los Angeles time.
  oodle genai datasets schedule set support-eval \
    --time 09:00 --weekday monday --weekday friday \
    --timezone America/Los_Angeles --dataset-id <id> \
    --connection-id <id> --prompt-name support-reply

  # Stop it firing without losing the definition.
  oodle genai datasets schedule set support-eval \
    --enabled=false -f schedule.json

--file takes the whole schedule body, with the experiment
config nested under "experimentConfig":

  {
    "enabled": true,
    "mode": "calendar",
    "timezone": "America/Los_Angeles",
    "times": ["09:00", "21:30"],
    "weekdays": ["monday", "friday"],
    "experimentConfig": {
      "datasetId": "<id>",
      "llmConnectionId": "<id>",
      "promptName": "support-reply",
      "model": "gpt-4o"
    }
  }

Flags override the file. --mode is inferred from --every or
--time when it is not given.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.SetGenaiDatasetScheduleJSONRequestBody
			config := map[string]any{}
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
				// The file's experimentConfig is the base the
				// flags are laid over, so a file plus one flag
				// does not drop the rest of the config.
				if existing, ok := body.ExperimentConfig.(map[string]any); ok {
					config = existing
				}
			}
			cfg.applyTo(config)
			if err := validateExperimentConfig(config); err != nil {
				return err
			}
			body.ExperimentConfig = config

			if cmd.Flags().Changed("enabled") || file == "" {
				body.Enabled = enabled
			}
			if err := applyScheduleShape(
				&body, cmd, mode, every, times,
				timezone, weekdays, daysOfMonth,
			); err != nil {
				return err
			}

			resp, err := c.Inner.SetGenaiDatasetScheduleWithResponse(
				cmd.Context(), getInstance(cmd), args[0], body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON200, scheduleColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the schedule",
	)
	cmd.Flags().BoolVar(
		&enabled, "enabled", true,
		"Whether the schedule fires; --enabled=false keeps the "+
			"definition but stops it running",
	)
	cmd.Flags().StringVar(
		&mode, "mode", "",
		"Schedule shape: calendar or interval (default: "+
			"inferred from --every or --time)",
	)
	cmd.Flags().StringVar(
		&every, "every", "",
		"Interval between runs, e.g. 30m, 6h or 1d "+
			"(at least 5m, at most 365d)",
	)
	cmd.Flags().StringSliceVar(
		&times, "time", nil,
		"Time of day to run, HH:MM in --timezone (repeatable)",
	)
	cmd.Flags().StringVar(
		&timezone, "timezone", "",
		"IANA timezone the times are read in (default UTC)",
	)
	cmd.Flags().StringSliceVar(
		&weekdays, "weekday", nil,
		"Weekday to run on, lowercase name (repeatable; "+
			"default: every day)",
	)
	cmd.Flags().StringSliceVar(
		&daysOfMonth, "day-of-month", nil,
		"Day of the month to run on, 1-31 (repeatable; "+
			"default: every day)",
	)
	cfg.addTo(cmd, false)
	return cmd
}

// applyScheduleShape overlays the timing flags onto body and
// settles which of the two shapes it is.
//
// The two field groups are mutually exclusive on the server, so
// naming both is a mistake worth reporting rather than
// resolving: whichever one is silently dropped is the one the
// caller expected to fire.
func applyScheduleShape(
	body *client.SetGenaiDatasetScheduleJSONRequestBody,
	cmd *cobra.Command,
	mode, every string,
	times []string,
	timezone string,
	weekdays, daysOfMonth []string,
) error {
	if every != "" && len(times) > 0 {
		return fmt.Errorf(
			"--every and --time are the two schedule shapes; " +
				"give one, not both",
		)
	}

	if mode == "" {
		switch {
		case every != "":
			mode = scheduleModeInterval
		case len(times) > 0:
			mode = scheduleModeCalendar
		}
	}
	if mode != "" {
		if mode != scheduleModeCalendar &&
			mode != scheduleModeInterval {
			return fmt.Errorf(
				"--mode must be %q or %q, got %q",
				scheduleModeCalendar, scheduleModeInterval, mode,
			)
		}
		body.Mode = &mode
	}

	if every != "" {
		value, unit, err := parseScheduleInterval(every)
		if err != nil {
			return err
		}
		body.IntervalValue = &value
		body.IntervalUnit = &unit
	}
	if len(times) > 0 {
		body.Times = &times
	}
	if timezone != "" {
		body.Timezone = &timezone
	}
	// An empty repeatable flag means "every day" on the server,
	// so only a supplied one is written — otherwise re-setting a
	// schedule from a file would clear its weekday filter.
	if cmd.Flags().Changed("weekday") {
		body.Weekdays = &weekdays
	}
	if cmd.Flags().Changed("day-of-month") {
		body.DaysOfMonth = &daysOfMonth
	}

	if body.Mode != nil && *body.Mode == scheduleModeCalendar &&
		(body.Times == nil || len(*body.Times) == 0) {
		return fmt.Errorf(
			"a calendar schedule needs at least one --time",
		)
	}
	if body.Mode != nil && *body.Mode == scheduleModeInterval &&
		body.IntervalValue == nil {
		return fmt.Errorf(
			"an interval schedule needs --every",
		)
	}
	return nil
}

// scheduleUnits maps the duration suffixes a person writes onto
// the unit names the API takes.
var scheduleUnits = map[byte]string{
	'm': "minutes",
	'h': "hours",
	'd': "days",
}

// parseScheduleInterval turns "6h" into 6 and "hours".
//
// The API takes a value and a unit rather than a duration, and
// only these three units, so a compound form like "1h30m" has
// no representation. Rejecting it beats rounding it into
// something that fires at a cadence nobody asked for.
func parseScheduleInterval(value string) (int, string, error) {
	v := strings.TrimSpace(value)
	malformed := fmt.Errorf(
		"invalid --every %q: expected a whole number and one of "+
			"m, h or d, such as 30m, 6h or 1d",
		value,
	)
	if len(v) < 2 {
		return 0, "", malformed
	}
	unit, ok := scheduleUnits[v[len(v)-1]]
	if !ok {
		return 0, "", malformed
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n <= 0 {
		return 0, "", malformed
	}
	return n, unit, nil
}

func newGenAIDatasetScheduleDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <dataset-name>",
		Short: "Delete a dataset's experiment schedule",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete the experiment schedule on dataset %q?",
				args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiDatasetScheduleWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"Deleted the schedule on dataset %s\n", args[0],
			)
			return nil
		},
	}
}
