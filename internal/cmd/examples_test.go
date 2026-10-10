package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestExamplesSetRequiredFlags checks that each example line of a command
// sets the flags that the command requires. An example that fails when
// someone runs it makes the reader think the feature does not work.
func TestExamplesSetRequiredFlags(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Example == "" {
			return
		}
		var required []string
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if ann := f.Annotations[cobra.BashCompOneRequiredFlag]; len(ann) > 0 && ann[0] == "true" {
				required = append(required, f.Name)
			}
		})
		path := strings.TrimPrefix(c.CommandPath(), "oodle ")
		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "oodle "+path) {
				continue
			}
			for _, name := range required {
				short := c.Flags().Lookup(name).Shorthand
				if strings.Contains(line, "--"+name) || (short != "" && strings.Contains(line, "-"+short+" ")) {
					continue
				}
				t.Errorf("%s: example %q does not set required flag --%s", c.CommandPath(), line, name)
			}
		}
	}
	walk(NewRootCmd())
}
