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

// fakeNerdGraph answers each query type with canned data; page 1 of the
// dashboard search returns a cursor so pagination is exercised.
func fakeNerdGraph(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("API-Key") != "NRAK-good" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Invalid API key"}]}`)
			return
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		q := req.Query
		var data string
		switch {
		case strings.Contains(q, "user { email }"):
			data = `{"actor":{"user":{"email":"a@b.c"}}}`
		case strings.Contains(q, "entitySearch") && req.Variables["cursor"] == nil:
			data = `{"actor":{"entitySearch":{"results":{"nextCursor":"p2","entities":[{"guid":"D1","dashboardParentGuid":null},{"guid":"P1","dashboardParentGuid":"D1"}]}}}}`
		case strings.Contains(q, "entitySearch"):
			data = `{"actor":{"entitySearch":{"results":{"nextCursor":null,"entities":[{"guid":"D2"}]}}}}`
		case strings.Contains(q, "entities(guids"):
			data = `{"actor":{"entities":[{"guid":"D1","name":"one"},{"guid":"D2","name":"two"}]}}`
		case strings.Contains(q, "accounts { id name }"):
			data = `{"actor":{"accounts":[{"id":1,"name":"ok"},{"id":2,"name":"denied"}]}}`
		case req.Variables["id"] == float64(2):
			io.WriteString(w, `{"data":null,"errors":[{"message":"Access denied"}]}`)
			return
		case strings.Contains(q, "policiesSearch"):
			data = `{"actor":{"account":{"alerts":{"policiesSearch":{"nextCursor":null,"policies":[{"id":"10","name":"p"}]}}}}}`
		case strings.Contains(q, "nrqlConditionsSearch"):
			data = `{"actor":{"account":{"alerts":{"nrqlConditionsSearch":{"nextCursor":null,"nrqlConditions":[{"id":"20"},{"id":"21"}]}}}}}`
		default:
			t.Fatalf("unexpected query: %s", q)
		}
		io.WriteString(w, `{"data":`+data+`}`)
	}))
}

func TestExport(t *testing.T) {
	srv := fakeNerdGraph(t)
	defer srv.Close()
	ctx := context.Background()

	bad, _ := NewClient(srv.URL, "NRAK-bad")
	if _, err := bad.Validate(ctx); err == nil {
		t.Fatal("expected validation to fail for a bad key")
	}
	if _, err := NewClient("not a url", "k"); err == nil {
		t.Fatal("expected invalid URL error")
	}

	c, _ := NewClient(srv.URL, "NRAK-good")
	if _, err := c.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := c.Export(ctx, dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r.Dashboards != 2 || r.Policies != 1 || r.Conditions != 2 || len(r.AccountErrs) != 1 {
		t.Fatalf("unexpected report: %+v", r)
	}
	for _, p := range []string{"dashboards/D1.json", "dashboards/D2.json", "alerts/1/policies/10.json", "alerts/1/conditions/21.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
}
