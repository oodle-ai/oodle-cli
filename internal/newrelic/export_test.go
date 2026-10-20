package newrelic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeNewRelic answers NerdGraph, REST v2 and Infrastructure API calls with
// canned data. Dashboard search, the REST APM list and the infra list each span
// two pages so every pagination style is exercised.
func fakeNewRelic(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("API-Key")
		if key != "NRAK-good" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Invalid API key"}]}`)
			return
		}
		q := r.URL.Query()
		switch r.URL.Path {
		case "/graphql":
		case "/v2/alerts_conditions.json":
			if q.Get("page") == "1" {
				w.Header().Set("Link", `<x?page=2>; rel="next"`)
				io.WriteString(w, `{"conditions":[{"id":30,"type":"apm_app_metric"}]}`)
			} else {
				io.WriteString(w, `{"conditions":[{"id":31,"type":"browser_metric"}]}`)
			}
			return
		case "/v2/alerts_external_service_conditions.json":
			io.WriteString(w, `{"external_service_conditions":[]}`)
			return
		case "/v2/alerts_synthetics_conditions.json":
			io.WriteString(w, `{"synthetics_conditions":[{"id":40,"monitor_id":"m"}]}`)
			return
		case "/v2/alerts_location_failure_conditions/policies/10.json":
			io.WriteString(w, `{"location_failure_conditions":[{"id":50}]}`)
			return
		case "/infra/v2/alerts/conditions":
			if q.Get("offset") == "0" {
				io.WriteString(w, `{"data":[{"id":60},{"id":61}],"meta":{"offset":0,"limit":2,"total":3}}`)
			} else {
				io.WriteString(w, `{"data":[{"id":62}],"meta":{"offset":2,"limit":2,"total":3}}`)
			}
			return
		default:
			t.Errorf("unexpected path %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gq := req.Query
		var data string
		switch {
		case strings.Contains(gq, "user { email }"):
			data = `{"actor":{"user":{"email":"a@b.c"}}}`
		case strings.Contains(gq, "entitySearch") && req.Variables["cursor"] == nil:
			data = `{"actor":{"entitySearch":{"results":{"nextCursor":"p2","entities":[{"guid":"D1","dashboardParentGuid":null},{"guid":"P1","dashboardParentGuid":"D1"}]}}}}`
		case strings.Contains(gq, "entitySearch"):
			data = `{"actor":{"entitySearch":{"results":{"nextCursor":null,"entities":[{"guid":"D2"}]}}}}`
		case strings.Contains(gq, "entities(guids"):
			data = `{"actor":{"entities":[{"guid":"D1","name":"one"},{"guid":"D2","name":"two"}]}}`
		case strings.Contains(gq, "accounts { id name }"):
			data = `{"actor":{"accounts":[{"id":1,"name":"ok"},{"id":2,"name":"denied"}]}}`
		case req.Variables["id"] == float64(2):
			io.WriteString(w, `{"data":null,"errors":[{"message":"Access denied"}]}`)
			return
		case strings.Contains(gq, "policiesSearch"):
			data = `{"actor":{"account":{"alerts":{"policiesSearch":{"nextCursor":null,"policies":[{"id":"10","name":"p"}]}}}}}`
		case strings.Contains(gq, "nrqlConditionsSearch"):
			data = `{"actor":{"account":{"alerts":{"nrqlConditionsSearch":{"nextCursor":null,"nrqlConditions":[{"id":"20"},{"id":"21"}]}}}}}`
		default:
			t.Fatalf("unexpected query: %s", gq)
		}
		io.WriteString(w, `{"data":`+data+`}`)
	}))
}

func TestExport(t *testing.T) {
	srv := fakeNewRelic(t)
	defer srv.Close()
	ctx := context.Background()

	bad, _ := NewClient(srv.URL+"/graphql", "NRAK-bad")
	if _, err := bad.Validate(ctx); err == nil {
		t.Fatal("expected validation to fail for a bad key")
	}
	if _, err := NewClient("not a url", "k"); err == nil {
		t.Fatal("expected invalid URL error")
	}

	c, _ := NewClient(srv.URL+"/graphql", "NRAK-good")
	if c.RESTURL != srv.URL+"/v2" {
		t.Fatalf("RESTURL = %s", c.RESTURL)
	}
	c.InfraURL = srv.URL + "/infra/v2"
	if _, err := c.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := c.Export(ctx, dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"nrql_conditions": 2, "apm_conditions": 2, "external_service_conditions": 0,
		"synthetics_conditions": 1, "location_failure_conditions": 1, "infra_conditions": 3}
	for k, n := range want {
		if r.Conditions[k] != n {
			t.Errorf("%s = %d, want %d", k, r.Conditions[k], n)
		}
	}
	if r.Dashboards != 2 || r.Policies != 1 || len(r.Warnings) != 1 {
		t.Fatalf("unexpected report: %+v", r)
	}
	for _, p := range []string{"dashboards/D1.json", "dashboards/D2.json", "alerts/1/policies/10.json",
		"alerts/1/nrql_conditions/21.json", "alerts/1/apm_conditions/31.json", "alerts/1/synthetics_conditions/40.json",
		"alerts/1/location_failure_conditions/50.json", "alerts/1/infra_conditions/62.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	for _, p := range []string{"alerts/2", "alerts/1/external_service_conditions"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist", p)
		}
	}
}
