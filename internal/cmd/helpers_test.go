package cmd

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

type sample struct {
	Foo string `json:"foo" yaml:"foo"`
	Bar int    `json:"bar" yaml:"bar"`
}

func TestReadInputFile_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.json")
	if err := os.WriteFile(path, []byte(`{"foo":"hi","bar":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "hi" || s.Bar != 7 {
		t.Errorf("got %+v", s)
	}
}

func TestReadInputFile_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(path, []byte("foo: hello\nbar: 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "hello" || s.Bar != 42 {
		t.Errorf("got %+v", s)
	}
}

func TestReadInputFile_UnknownExtensionFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(path, []byte(`{"foo":"x","bar":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "x" || s.Bar != 1 {
		t.Errorf("got %+v", s)
	}
}

// timeFlagVariant captures the unit-specific knobs that distinguish
// parseTimeFlag (microseconds) from parseTimeFlagMs (milliseconds). Each
// table case below runs the same checks against both parsers so they cannot
// drift out of sync.
type timeFlagVariant struct {
	name      string
	parse     func(string) (int64, error)
	now       func() int64                // current epoch in the variant's unit
	epochLit  string                      // a sample integer literal
	epochInt  int64                       // the same literal, parsed
	tolerance int64                       // ±tolerance for relative-time checks
	durToUnit func(d time.Duration) int64 // convert a duration to the variant's unit
}

func timeFlagVariants() []timeFlagVariant {
	return []timeFlagVariant{
		{
			name:      "Micro",
			parse:     parseTimeFlag,
			now:       func() int64 { return time.Now().UnixMicro() },
			epochLit:  "1700000000000000",
			epochInt:  1700000000000000,
			tolerance: int64(time.Second / time.Microsecond),
			durToUnit: func(d time.Duration) int64 { return int64(d / time.Microsecond) },
		},
		{
			name:      "Milli",
			parse:     parseTimeFlagMs,
			now:       func() int64 { return time.Now().UnixMilli() },
			epochLit:  "1700000000000",
			epochInt:  1700000000000,
			tolerance: int64(time.Second / time.Millisecond),
			durToUnit: func(d time.Duration) int64 { return int64(d / time.Millisecond) },
		},
	}
}

func TestParseTimeFlag_Epoch(t *testing.T) {
	for _, v := range timeFlagVariants() {
		t.Run(v.name, func(t *testing.T) {
			got, err := v.parse(v.epochLit)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.epochInt {
				t.Errorf("got %d, want %d", got, v.epochInt)
			}
		})
	}
}

func TestParseTimeFlag_Now(t *testing.T) {
	for _, v := range timeFlagVariants() {
		t.Run(v.name, func(t *testing.T) {
			before := v.now()
			got, err := v.parse("now")
			if err != nil {
				t.Fatal(err)
			}
			after := v.now()
			if got < before || got > after {
				t.Errorf("now = %d not in [%d, %d]", got, before, after)
			}
		})
	}
}

func TestParseTimeFlag_Relative1h(t *testing.T) {
	for _, v := range timeFlagVariants() {
		t.Run(v.name, func(t *testing.T) {
			now := v.now()
			got, err := v.parse("-1h")
			if err != nil {
				t.Fatal(err)
			}
			delta := now - got
			expected := v.durToUnit(time.Hour)
			if delta < expected-v.tolerance || delta > expected+v.tolerance {
				t.Errorf("delta = %d, want ~%d", delta, expected)
			}
		})
	}
}

func TestParseTimeFlag_Relative7d(t *testing.T) {
	for _, v := range timeFlagVariants() {
		t.Run(v.name, func(t *testing.T) {
			now := v.now()
			got, err := v.parse("-7d")
			if err != nil {
				t.Fatal(err)
			}
			delta := now - got
			expected := v.durToUnit(7 * 24 * time.Hour)
			if delta < expected-v.tolerance || delta > expected+v.tolerance {
				t.Errorf("delta = %d, want ~%d", delta, expected)
			}
		})
	}
}

func TestParseTimeFlag_Invalid(t *testing.T) {
	for _, v := range timeFlagVariants() {
		t.Run(v.name, func(t *testing.T) {
			if _, err := v.parse("garbage"); err == nil {
				t.Error("expected error")
			}
			if _, err := v.parse(""); err == nil {
				t.Error("expected error for empty")
			}
		})
	}
}

// TestParseTimeFlagMs_UnitConversion confirms that the millisecond variant
// returns ms (not µs) for the "now" path, guarding against accidentally
// swapping the unit converter in parseTimeFlagAs.
func TestParseTimeFlagMs_UnitConversion(t *testing.T) {
	got, err := parseTimeFlagMs("now")
	if err != nil {
		t.Fatal(err)
	}
	nowMs := time.Now().UnixMilli()
	// got should be within ~1s of nowMs; if the converter were UnixMicro
	// this would be off by ~1000x.
	if got < nowMs-1000 || got > nowMs+1000 {
		t.Errorf("parseTimeFlagMs(\"now\") = %d not within 1s of %d (likely wrong unit)", got, nowMs)
	}
}

// --- parseTimeFlagSeconds tests ---
// Consolidated into a single test with subtests for consistency with the
// table-driven pattern used by parseTimeFlag / parseTimeFlagMs tests above.

func TestParseTimeFlagSeconds(t *testing.T) {
	const tolerance = 2.0 // seconds

	t.Run("Epoch", func(t *testing.T) {
		got, err := parseTimeFlagSeconds("1700000000")
		if err != nil {
			t.Fatal(err)
		}
		if got != 1700000000 {
			t.Errorf("got %v, want 1700000000", got)
		}
	})

	t.Run("EpochFloat", func(t *testing.T) {
		got, err := parseTimeFlagSeconds("1700000000.5")
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(got-1700000000.5) > 0.001 {
			t.Errorf("got %v, want 1700000000.5", got)
		}
	})

	t.Run("Now", func(t *testing.T) {
		before := float64(time.Now().Unix())
		got, err := parseTimeFlagSeconds("now")
		if err != nil {
			t.Fatal(err)
		}
		after := float64(time.Now().Unix())
		if got < before || got > after+1 {
			t.Errorf("now = %v not in [%v, %v]", got, before, after)
		}
	})

	t.Run("Relative1h", func(t *testing.T) {
		now := float64(time.Now().Unix())
		got, err := parseTimeFlagSeconds("-1h")
		if err != nil {
			t.Fatal(err)
		}
		delta := now - got
		expected := 3600.0
		if delta < expected-tolerance || delta > expected+tolerance {
			t.Errorf("delta = %v, want ~%v", delta, expected)
		}
	})

	t.Run("Relative7d", func(t *testing.T) {
		now := float64(time.Now().Unix())
		got, err := parseTimeFlagSeconds("-7d")
		if err != nil {
			t.Fatal(err)
		}
		delta := now - got
		expected := 7 * 24 * 3600.0
		if delta < expected-tolerance || delta > expected+tolerance {
			t.Errorf("delta = %v, want ~%v", delta, expected)
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		if _, err := parseTimeFlagSeconds("garbage"); err == nil {
			t.Error("expected error for 'garbage'")
		}
		if _, err := parseTimeFlagSeconds(""); err == nil {
			t.Error("expected error for empty string")
		}
	})
}

// --- parseTimeFlagSec tests ---
// parseTimeFlagSec returns whole epoch seconds, distinguishing it from
// parseTimeFlagSeconds (float64) and parseTimeFlagMs (milliseconds).

func TestParseTimeFlagSec(t *testing.T) {
	const tolerance = 2 // seconds

	t.Run("Epoch", func(t *testing.T) {
		got, err := parseTimeFlagSec("1700000000")
		if err != nil {
			t.Fatal(err)
		}
		if got != 1700000000 {
			t.Errorf("got %d, want 1700000000", got)
		}
	})

	t.Run("UnitConversion", func(t *testing.T) {
		got, err := parseTimeFlagSec("now")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Unix()
		if got < now-tolerance || got > now+tolerance {
			t.Errorf("parseTimeFlagSec(\"now\") = %d not within %ds of %d (likely wrong unit)", got, tolerance, now)
		}
	})

	t.Run("Relative7d", func(t *testing.T) {
		got, err := parseTimeFlagSec("-7d")
		if err != nil {
			t.Fatal(err)
		}
		delta := time.Now().Unix() - got
		const expected = 7 * 24 * 3600
		if delta < expected-tolerance || delta > expected+tolerance {
			t.Errorf("delta = %d, want ~%d", delta, expected)
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		if _, err := parseTimeFlagSec("garbage"); err == nil {
			t.Error("expected error for 'garbage'")
		}
		if _, err := parseTimeFlagSec(""); err == nil {
			t.Error("expected error for empty string")
		}
	})
}

func TestConfirmAction_Force(t *testing.T) {
	if !confirmAction("delete?", true) {
		t.Error("force=true should return true")
	}
}

// TestReadInputFile_YAMLPopulatesOneOfUnion pins the transcode in
// unmarshalYAMLAsJSON. Decoded by gopkg.in/yaml.v3 directly, typeSpecificData
// comes back nil — yaml.v3 honours neither the `json` tags nor the union's
// custom UnmarshalJSON — and the CLI would send a create request with its
// entire type-specific body silently missing.
func TestReadInputFile_YAMLPopulatesOneOfUnion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integration.yaml")
	body := "" +
		"type: AZURE_METRICS\n" +
		"typeSpecificData:\n" +
		"  azureMetricsIntegration:\n" +
		"    subscriptionName: from-yaml\n" +
		"    tenantId: tenant-abc\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var req client.CreateIntegrationsJSONRequestBody
	if err := readInputFile(path, &req); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if req.Type != "AZURE_METRICS" {
		t.Errorf("Type = %q, want AZURE_METRICS", req.Type)
	}
	if req.TypeSpecificData == nil {
		t.Fatal("TypeSpecificData is nil: YAML dropped the union payload")
	}
	wrapper, err := req.TypeSpecificData.AsAzureMetricsIntegrationWrapper()
	if err != nil {
		t.Fatalf("decoding union: %v", err)
	}
	if wrapper.AzureMetricsIntegration == nil {
		t.Fatal("azureMetricsIntegration is nil")
	}
	if got := wrapper.AzureMetricsIntegration.SubscriptionName; got == nil || *got != "from-yaml" {
		t.Errorf("SubscriptionName = %v, want from-yaml", got)
	}
}

func TestEpochToUnit(t *testing.T) {
	const sec = int64(1700000000)
	inputs := []int64{sec, sec * 1e3, sec * 1e6, sec * 1e9}
	for _, perSec := range []int64{1, 1e3, 1e6} {
		for _, n := range inputs {
			if got, want := epochToUnit(n, perSec), sec*perSec; got != want {
				t.Errorf("epochToUnit(%d, %d) = %d, want %d", n, perSec, got, want)
			}
		}
	}
	if got := epochToUnit(0, 1e6); got != 0 {
		t.Errorf("epochToUnit(0) = %d, want 0", got)
	}
}

// TestParseTimeFlag_WrongUnit checks that an epoch in another unit is
// converted. Without this, a range in the wrong unit returns no data.
func TestParseTimeFlag_WrongUnit(t *testing.T) {
	const sec = int64(1700000000)
	for _, lit := range []string{"1700000000", "1700000000000", "1700000000000000", "1700000000000000000"} {
		if got, _ := parseTimeFlag(lit); got != sec*1e6 {
			t.Errorf("parseTimeFlag(%s) = %d, want %d", lit, got, sec*1e6)
		}
		if got, _ := parseTimeFlagMs(lit); got != sec*1e3 {
			t.Errorf("parseTimeFlagMs(%s) = %d, want %d", lit, got, sec*1e3)
		}
		if got, _ := parseTimeFlagSec(lit); got != sec {
			t.Errorf("parseTimeFlagSec(%s) = %d, want %d", lit, got, sec)
		}
		if got, _ := parseTimeFlagSeconds(lit); got != float64(sec) {
			t.Errorf("parseTimeFlagSeconds(%s) = %v, want %d", lit, got, sec)
		}
	}
	if got, _ := parseTimeFlagSeconds("1700000000.5"); got != 1700000000.5 {
		t.Errorf("fractional seconds changed: %v", got)
	}
}

func TestParseTimeFlag_RFC3339(t *testing.T) {
	const lit = "2023-11-14T22:13:20Z" // 1700000000
	if got, err := parseTimeFlag(lit); err != nil || got != 1700000000*1e6 {
		t.Errorf("parseTimeFlag(%s) = %d, %v", lit, got, err)
	}
	if got, err := parseTimeFlagSeconds(lit); err != nil || got != 1700000000 {
		t.Errorf("parseTimeFlagSeconds(%s) = %v, %v", lit, got, err)
	}
}
