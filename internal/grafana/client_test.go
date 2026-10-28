package grafana

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestGetDashboardsPagesThroughSearch verifies that GetDashboards walks every
// page of the Grafana search API. The API returns at most `limit` rows and only
// the first page when `page` is absent, so a single request would silently drop
// dashboards beyond the first page.
func TestGetDashboardsPagesThroughSearch(t *testing.T) {
	var pages []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			t.Errorf("path = %q, want /api/search", r.URL.Path)
		}
		if got := r.URL.Query().Get("type"); got != "dash-db" {
			t.Errorf("type = %q, want dash-db", got)
		}

		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Errorf("page param = %q, want an integer", r.URL.Query().Get("page"))
			page = 1
		}
		pages = append(pages, page)

		// First page is full, second page holds the final dashboard.
		count := 1
		if page == 1 {
			count = dashboardSearchPageSize
		}
		dashboards := make([]Dashboard, 0, count)
		for i := 0; i < count; i++ {
			dashboards = append(dashboards, Dashboard{
				UID:   fmt.Sprintf("dash-%d-%d", page, i),
				Title: fmt.Sprintf("Dashboard %d-%d", page, i),
			})
		}
		writeJSON(t, w, dashboards)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	got, err := c.GetDashboards(context.Background())
	if err != nil {
		t.Fatalf("GetDashboards: %v", err)
	}

	if want := dashboardSearchPageSize + 1; len(got) != want {
		t.Errorf("returned %d dashboards, want %d", len(got), want)
	}
	if want := []int{1, 2}; len(pages) != len(want) || pages[0] != want[0] || pages[1] != want[1] {
		t.Errorf("pages requested = %v, want %v", pages, want)
	}
}

// TestGetDashboardsStopsOnShortPage verifies that a page smaller than the page
// size ends the walk after a single request.
func TestGetDashboardsStopsOnShortPage(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeJSON(t, w, []Dashboard{{UID: "only-one", Title: "Only One"}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	got, err := c.GetDashboards(context.Background())
	if err != nil {
		t.Fatalf("GetDashboards: %v", err)
	}

	if len(got) != 1 {
		t.Errorf("returned %d dashboards, want 1", len(got))
	}
	if requests != 1 {
		t.Errorf("made %d search requests, want 1", requests)
	}
}

// TestGetDashboardsStopsOnEmptyPage verifies that an empty first page ends the
// walk without looping forever.
func TestGetDashboardsStopsOnEmptyPage(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeJSON(t, w, []Dashboard{})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	got, err := c.GetDashboards(context.Background())
	if err != nil {
		t.Fatalf("GetDashboards: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("returned %d dashboards, want 0", len(got))
	}
	if requests != 1 {
		t.Errorf("made %d search requests, want 1", requests)
	}
}
