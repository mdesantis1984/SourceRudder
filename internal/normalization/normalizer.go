package normalization

import (
	"net/url"
	"strings"
)

func NormalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	path := strings.TrimSuffix(u.Path, "/")

	normalized := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Path:   path,
	}

	return normalized.String()
}

func ExtractCanonicalURL(rawURL string) string {
	return NormalizeURL(rawURL)
}

func DeduplicateResults(results []string) []string {
	seen := make(map[string]bool)
	deduped := make([]string, 0, len(results))

	for _, r := range results {
		canonical := ExtractCanonicalURL(r)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		deduped = append(deduped, r)
	}

	return deduped
}
