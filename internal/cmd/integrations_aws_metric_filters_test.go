package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

func TestParseMetricFilterFlags(t *testing.T) {
	filters, err := parseMetricFilterFlags([]string{
		"AWS/EC2:include=aws_ec2_cpuutilization_maximum, aws_ec2_network*",
		"AWS/RDS:exclude=*_samplecount",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(filters) != 2 {
		t.Fatalf("got %d filters, want 2", len(filters))
	}
	if filters[0].Namespace != "AWS/EC2" || filters[0].IncludeOnly == nil ||
		!reflect.DeepEqual(*filters[0].IncludeOnly, []string{"aws_ec2_cpuutilization_maximum", "aws_ec2_network*"}) ||
		filters[0].ExcludeOnly != nil {
		t.Errorf("filters[0] = %+v", filters[0])
	}
	if filters[1].Namespace != "AWS/RDS" || filters[1].ExcludeOnly == nil ||
		!reflect.DeepEqual(*filters[1].ExcludeOnly, []string{"*_samplecount"}) ||
		filters[1].IncludeOnly != nil {
		t.Errorf("filters[1] = %+v", filters[1])
	}

	for _, bad := range [][]string{
		{"AWS/EC2"},
		{"AWS/EC2:keep=aws_ec2_cpu*"},
		{":include=aws_ec2_cpu*"},
		{"AWS/EC2:include="},
		{"AWS/EC2:include=A", "AWS/EC2:exclude=B"},
	} {
		if _, err := parseMetricFilterFlags(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestBuildCloudWatchPayloadMetricFilters(t *testing.T) {
	without, err := buildCloudWatchPayload(awsBuildInputs{namespaces: []string{"AWS/EC2"}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if without.MetricNameFilters != nil {
		t.Errorf("MetricNameFilters = %v, want nil without --metric-filter", without.MetricNameFilters)
	}

	with, err := buildCloudWatchPayload(awsBuildInputs{
		namespaces:    []string{"AWS/EC2"},
		metricFilters: []string{"AWS/EC2:include=aws_ec2_cpu*"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if with.MetricNameFilters == nil || len(*with.MetricNameFilters) != 1 {
		t.Errorf("MetricNameFilters = %v, want 1 filter", with.MetricNameFilters)
	}
}

// runAwsUpdate runs `aws update` against a server whose integration has one
// metric filter, and returns the metricNameFilters value that the PATCH
// sent. The bool is false when the field was absent from the body.
func runAwsUpdate(t *testing.T, args ...string) (json.RawMessage, bool) {
	t.Helper()
	var body struct {
		TypeSpecificData struct {
			CloudWatchMetricPullIntegration map[string]json.RawMessage `json:"cloudWatchMetricPullIntegration"`
		} `json:"typeSpecificData"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"id":"int-1","type":"CLOUDWATCH_METRIC_PULL",
				"typeSpecificData":{"cloudWatchMetricPullIntegration":{
				   "accountId":"123","externalId":"e","roleArn":"arn:aws:iam::123:role/R",
				   "regions":["us-west-2"],
				   "resourceTypesSearchTagsList":[{"resourceTypes":["AWS/EC2"]}],
				   "metricNameFilters":[{"namespace":"AWS/EC2","includeOnly":["aws_ec2_cpu*"]}]
				}}
			}`)
		case http.MethodPatch:
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"int-1"}`)
		}
	}))
	defer srv.Close()

	cmd := newAwsUpdateCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetContext(awsCtxWith(t, srv.URL))
	cmd.SetArgs(append([]string{"int-1"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filters, ok := body.TypeSpecificData.CloudWatchMetricPullIntegration["metricNameFilters"]
	return filters, ok
}

func TestAwsUpdateCmd_KeepsMetricFilters(t *testing.T) {
	filters, ok := runAwsUpdate(t, "--regions", "us-east-1")
	if !ok {
		t.Fatal("metricNameFilters missing from the PATCH body")
	}
	var got []client.CloudWatchMetricNameFilter
	if err := json.Unmarshal(filters, &got); err != nil {
		t.Fatalf("decode filters: %v", err)
	}
	if len(got) != 1 || got[0].Namespace != "AWS/EC2" || got[0].IncludeOnly == nil || (*got[0].IncludeOnly)[0] != "aws_ec2_cpu*" {
		t.Errorf("filters = %s, want the stored filter", filters)
	}
}

func TestAwsUpdateCmd_ReplacesMetricFilters(t *testing.T) {
	filters, _ := runAwsUpdate(t, "--metric-filter", "AWS/RDS:exclude=*_samplecount")
	var got []client.CloudWatchMetricNameFilter
	if err := json.Unmarshal(filters, &got); err != nil {
		t.Fatalf("decode filters: %v", err)
	}
	if len(got) != 1 || got[0].Namespace != "AWS/RDS" || got[0].ExcludeOnly == nil {
		t.Errorf("filters = %s, want only the AWS/RDS filter", filters)
	}
}

func TestAwsUpdateCmd_ClearsMetricFilters(t *testing.T) {
	filters, ok := runAwsUpdate(t, "--clear-metric-filters")
	if !ok || string(filters) != "[]" {
		t.Errorf("metricNameFilters = %s (present %v), want []", filters, ok)
	}
}
