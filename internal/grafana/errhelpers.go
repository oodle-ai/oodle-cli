package grafana

import "fmt"

// wrapErr and wrapErrf mirror the small subset of github.com/cockroachdb/errors
// used by the ported Grafana exporter, backed by the standard library so this
// package adds no extra dependency.
func wrapErr(err error, msg string) error {
	return fmt.Errorf("%s: %w", msg, err)
}

func wrapErrf(err error, format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, err)...)
}
