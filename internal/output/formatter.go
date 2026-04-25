package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// Format identifies an output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatCSV   Format = "csv"
)

// Column describes one column of a table/CSV. Field is the Go struct field
// name (case-insensitive match).
type Column struct {
	Header string
	Field  string
}

// Print writes data in the requested format to w. For table and CSV, data
// must be a slice; single objects can be wrapped in a one-element slice. If
// table or CSV is requested but no columns are defined (e.g. for free-form
// map[string]interface{} responses), Print falls back to JSON so the user
// sees something useful rather than a blank screen.
func Print(w io.Writer, format Format, data any, columns []Column) error {
	switch format {
	case FormatJSON, "":
		return printJSON(w, data)
	case FormatYAML:
		return printYAML(w, data)
	case FormatCSV:
		if len(columns) == 0 {
			return printJSON(w, data)
		}
		return printCSV(w, data, columns)
	case FormatTable:
		if len(columns) == 0 {
			return printJSON(w, data)
		}
		return printTable(w, data, columns)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

func printJSON(w io.Writer, data any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

func printYAML(w io.Writer, data any) error {
	out, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding yaml: %w", err)
	}
	_, err = w.Write(out)
	return err
}

// asSlice reflects a value to a []reflect.Value, wrapping a single value in a
// one-element slice if necessary.
func asSlice(data any) ([]reflect.Value, error) {
	v := reflect.ValueOf(data)
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]reflect.Value, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = v.Index(i)
		}
		return out, nil
	case reflect.Struct, reflect.Map:
		return []reflect.Value{v}, nil
	case reflect.Invalid:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported type for tabular output: %s", v.Kind())
	}
}

// fieldValue extracts the value of `field` from v as a string. It looks up
// struct fields case-insensitively and dereferences pointers/interfaces.
func fieldValue(v reflect.Value, field string) string {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		// Case-insensitive field match.
		f := v.FieldByNameFunc(func(name string) bool {
			return strings.EqualFold(name, field)
		})
		if !f.IsValid() {
			return ""
		}
		return formatScalar(f)
	case reflect.Map:
		// Case-insensitive map lookup if keys are strings.
		if v.Type().Key().Kind() == reflect.String {
			for _, k := range v.MapKeys() {
				if strings.EqualFold(k.String(), field) {
					return formatScalar(v.MapIndex(k))
				}
			}
		}
		return ""
	default:
		return formatScalar(v)
	}
}

func formatScalar(v reflect.Value) string {
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return ""
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%g", v.Float())
	default:
		// Fall back to fmt for slices/maps/structs.
		return fmt.Sprintf("%v", v.Interface())
	}
}

func printTable(w io.Writer, data any, columns []Column) error {
	rows, err := asSlice(data)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = c.Header
	}
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = fieldValue(row, c.Field)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func printCSV(w io.Writer, data any, columns []Column) error {
	rows, err := asSlice(data)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = c.Header
	}
	if err := cw.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, c := range columns {
			cells[i] = fieldValue(row, c.Field)
		}
		if err := cw.Write(cells); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// DetectFormat returns the format to use given the value of an --output flag.
// If the flag is non-empty it wins. Otherwise we pick `table` for TTYs and
// `json` for non-TTY output (pipes, files).
func DetectFormat(outputFlag string) Format {
	if outputFlag != "" {
		return Format(strings.ToLower(outputFlag))
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return FormatTable
	}
	return FormatJSON
}
