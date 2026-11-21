package connectors

import (
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// relaxMaxTerms is the number of non-qualifier terms kept when a query is shortened.
const relaxMaxTerms = 4

var relaxStopwords = func() map[string]struct{} {
	words := []string{
		// English
		"a", "an", "the", "and", "or", "of", "to", "in", "on", "at", "for", "with", "from", "by", "as", "is", "are",
		"was", "were", "be", "been", "it", "its", "this", "that", "these", "those", "how", "what", "which", "who",
		"why", "when", "where", "do", "does", "did", "can", "could", "should", "would", "i", "my", "me", "we", "you",
		"your", "not", "no", "into", "about", "using", "use",
		// Spanish
		"el", "la", "los", "las", "un", "una", "unos", "unas", "y", "o", "de", "del", "al", "en", "con", "sin", "por",
		"para", "que", "como", "cómo", "cual", "cuál", "quien", "quién", "cuando", "cuándo", "donde", "dónde", "es",
		"son", "ser", "se", "su", "sus", "mi", "mis", "lo", "le", "les", "esto", "esta", "este", "eso", "esa", "ese",
		"sobre", "entre", "hacia", "desde", "usando", "usar",
	}
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}()

// relaxQuery shortens a search query: every qualifier token (one containing
// ':') is kept, English and Spanish stopwords are dropped, and only the first
// relaxMaxTerms remaining terms are kept. Original token order is preserved.
func relaxQuery(q string) string {
	kept := make([]string, 0, relaxMaxTerms+2)
	terms := 0
	for _, token := range strings.Fields(q) {
		if strings.Contains(token, ":") {
			kept = append(kept, token)
			continue
		}
		if _, stop := relaxStopwords[strings.ToLower(token)]; stop {
			continue
		}
		if terms >= relaxMaxTerms {
			continue
		}
		terms++
		kept = append(kept, token)
	}
	return strings.Join(kept, " ")
}

// relaxedRetry runs fetch once more with a shortened query when the first
// attempt succeeded with zero results. It returns the (possibly replaced)
// results, the shortened query (empty when no retry ran) and the error of the
// attempt that produced the results. It never retries after an error or when
// the shortened query is empty or identical to the original.
func relaxedRetry(
	query string,
	results []types.SearchResultItem,
	err error,
	fetch func(q string) ([]types.SearchResultItem, error),
) ([]types.SearchResultItem, string, error) {
	if err != nil || len(results) > 0 {
		return results, "", err
	}
	shortened := relaxQuery(query)
	if shortened == "" || shortened == strings.Join(strings.Fields(query), " ") {
		return results, "", nil
	}
	retried, retryErr := fetch(shortened)
	return retried, shortened, retryErr
}

// applyRelaxWarning records that the response was produced by a shortened query.
func applyRelaxWarning(resp *types.SearchResponse, shortened string) {
	if shortened != "" {
		resp.Warnings = append(resp.Warnings, "query relaxed: "+shortened)
	}
}

// searchCacheCap returns the maximum cache lifetime for a final connector
// response: empty and partial responses are capped at DegradedSearchCacheTTL,
// healthy non-empty ones keep the cache service TTL (0 means no cap).
func searchCacheCap(resp *types.SearchResponse) time.Duration {
	if len(resp.Results) == 0 || resp.Partial {
		return DegradedSearchCacheTTL
	}
	return 0
}
