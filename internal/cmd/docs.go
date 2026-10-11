package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// The docs site at https://docs.oodle.ai serves this search
// application ID and key to each browser. The key can only search
// the public docs index; it cannot change the index.
const (
	docsSearchAppID = "0MH2XQ3KUF"
	docsSearchKey   = "ae30d72c1fbe373b1d15784c47a6a6a8"
	docsSearchIndex = "oodle"
)

// docsSearchURL is a variable so that tests can send the search to a
// local server.
var docsSearchURL = "https://" + docsSearchAppID + "-dsn.algolia.net/1/indexes/" + docsSearchIndex + "/query"

const (
	docsDefaultLimit   = 5
	docsMaxLimit       = 20
	docsSnippetChars   = 300
	docsTableSnippet   = 80
	docsSearchMaxBytes = 4 << 20
)

type docsHit struct {
	URL              string             `json:"url"`
	URLWithoutAnchor string             `json:"url_without_anchor"`
	Anchor           string             `json:"anchor"`
	Content          *string            `json:"content"`
	Hierarchy        map[string]*string `json:"hierarchy"`
}

type docsSearchResponse struct {
	Hits   []docsHit `json:"hits"`
	NbHits int       `json:"nbHits"`
}

// docsResult is one docs page in the search results.
type docsResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// docsLevels are the heading levels of a hit, from the top.
var docsLevels = []string{"lvl0", "lvl1", "lvl2", "lvl3", "lvl4", "lvl5", "lvl6"}

// cleanHeading removes the zero-width spaces that the docs site puts
// in some headings.
func cleanHeading(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(*s, "​", ""))
}

// groupDocsHits makes one result for each page. The search returns
// one hit for each section, so without this a page can fill all the
// results. The first hit of a page gives its title; the first hit
// with text gives the snippet.
func groupDocsHits(hits []docsHit, limit int) []docsResult {
	var out []docsResult
	index := map[string]int{}
	for _, h := range hits {
		page := h.URLWithoutAnchor
		if page == "" {
			page = h.URL
		}
		if page == "" {
			continue
		}
		snippet := ""
		if h.Content != nil {
			snippet = strings.Join(strings.Fields(*h.Content), " ")
		}
		if i, ok := index[page]; ok {
			if out[i].Snippet == "" {
				out[i].Snippet = preview(snippet, docsSnippetChars)
			}
			continue
		}
		if len(out) >= limit {
			continue
		}
		var parts []string
		for _, l := range docsLevels {
			if v := cleanHeading(h.Hierarchy[l]); v != "" {
				parts = append(parts, v)
			}
		}
		index[page] = len(out)
		out = append(out, docsResult{
			Title:   strings.Join(parts, " > "),
			URL:     page,
			Snippet: preview(snippet, docsSnippetChars),
		})
	}
	return out
}

func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Search the Oodle documentation",
	}
	cmd.AddCommand(newDocsSearchCmd())
	return cmd
}

func newDocsSearchCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the Oodle documentation at docs.oodle.ai",
		Long: `Search the Oodle documentation at https://docs.oodle.ai. Each result
is one page, with its title, URL and a short snippet. This command
does not need Oodle credentials.`,
		Example: `  oodle docs search "log metrics"
  oodle docs search "send logs with vector" --limit 10 -o json`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.TrimSpace(args[0])
			if query == "" {
				return fmt.Errorf("the query is empty")
			}
			if limit < 1 || limit > docsMaxLimit {
				return fmt.Errorf("--limit must be between 1 and %d, got %d", docsMaxLimit, limit)
			}
			// Ask for more hits than pages, because hits on the same
			// page are merged into one result.
			reqBody, err := json.Marshal(map[string]any{
				"query":                 query,
				"hitsPerPage":           min(limit*4, 50),
				"attributesToRetrieve":  []string{"url", "url_without_anchor", "anchor", "content", "hierarchy"},
				"attributesToHighlight": []string{},
			})
			if err != nil {
				return fmt.Errorf("encoding request: %w", err)
			}
			req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, docsSearchURL, bytes.NewReader(reqBody))
			if err != nil {
				return fmt.Errorf("building request: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Algolia-Application-Id", docsSearchAppID)
			req.Header.Set("X-Algolia-API-Key", docsSearchKey)
			resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				return fmt.Errorf("docs search failed: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(io.LimitReader(resp.Body, docsSearchMaxBytes))
			if err != nil {
				return fmt.Errorf("reading response: %w", err)
			}
			if resp.StatusCode >= 300 {
				return fmt.Errorf("docs search failed with status %d: %s", resp.StatusCode, strings.TrimSpace(preview(string(body), 200)))
			}
			var parsed docsSearchResponse
			if err := json.Unmarshal(body, &parsed); err != nil {
				return fmt.Errorf("parsing response: %w", err)
			}
			results := groupDocsHits(parsed.Hits, limit)
			if results == nil {
				results = []docsResult{}
			}
			if isTabular(cmd) {
				rows := make([]docsResult, len(results))
				for i, r := range results {
					r.Snippet = preview(r.Snippet, docsTableSnippet)
					rows[i] = r
				}
				err = output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), rows, []output.Column{
					{Header: "TITLE", Field: "Title"},
					{Header: "URL", Field: "URL"},
					{Header: "SNIPPET", Field: "Snippet"},
				})
			} else {
				err = printShaped(cmd, results)
			}
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "No docs pages match %q. Try other words, or browse https://docs.oodle.ai.\n", query)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", docsDefaultLimit, fmt.Sprintf("Maximum number of pages (1 to %d)", docsMaxLimit))
	return cmd
}
