package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// scrapeCounter fetches the prometheus exposition body for the given
// metric name. It is intentionally minimal — it only parses enough of
// the text format to let the tests assert on label sets and counter
// values.
func scrapeCounter(t testingT, h http.Handler, metric string) string {
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("scrape body: %v", err)
	}
	out := string(body)
	if !strings.Contains(out, metric) {
		t.Fatalf("expected metric %q in scrape body, got:\n%s", metric, out)
	}
	return out
}

func findMetricLine(body, labels string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, labels) && strings.Contains(line, "ia_buscar_search_degraded_total") {
			return line
		}
	}
	return ""
}

type testingT interface {
	Fatalf(format string, args ...interface{})
}