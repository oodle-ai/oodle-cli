package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// routedRequest is one request that the routed test server saw.
type routedRequest struct {
	method string
	path   string
	query  url.Values
	body   string
	apiKey string
	inst   string
}

// routedServer replies by the request path. A path that has no reply gets
// 404, so that a test fails when a command calls a route it must not call.
type routedServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []routedRequest
}

// newRoutedServer returns a server that replies with replies[path]. A
// PromQL query gets the reply of a promReplies key that is part of
// the query text, or an empty matrix.
func newRoutedServer(t *testing.T, replies map[string]string, promReplies map[string]string) *routedServer {
	t.Helper()
	rs := &routedServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.ParseForm()
		rs.mu.Lock()
		rs.requests = append(rs.requests, routedRequest{
			method: r.Method,
			path:   r.URL.Path,
			query:  r.Form,
			body:   string(body),
			apiKey: r.Header.Get("X-API-Key"),
			inst:   r.Header.Get("OODLE-INSTANCE"),
		})
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/v1/query") {
			q := r.Form.Get("query")
			for substr, reply := range promReplies {
				if strings.Contains(q, substr) {
					_, _ = fmt.Fprint(w, reply)
					return
				}
			}
			_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
			return
		}
		reply, ok := replies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"message":"not found"}`)
			return
		}
		_, _ = fmt.Fprint(w, reply)
	}))
	t.Cleanup(rs.Close)
	return rs
}

// find returns the requests with the given path.
func (rs *routedServer) find(path string) []routedRequest {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []routedRequest
	for _, r := range rs.requests {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func TestPromQuoteEscapesQuotes(t *testing.T) {
	if got := promQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("promQuote = %s", got)
	}
}

func TestPromSeriesPointsDropsNaN(t *testing.T) {
	s := promSeries{Values: [][2]any{{float64(1), "2"}, {float64(2), "NaN"}, {float64(3), "x"}, {float64(4), "5"}}}
	pts := s.points()
	if len(pts) != 2 || pts[0].value != 2 || pts[1].ts != 4 {
		t.Errorf("points = %+v", pts)
	}
}
