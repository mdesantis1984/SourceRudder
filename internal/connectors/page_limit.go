package connectors

// ResolveMaxResults returns the page size a search tool applies for a request:
// the documented default (20 for search_web, 10 otherwise) when the caller
// omits maxResults, capped at 100 (50 for the local index).
func ResolveMaxResults(source string, requested int) int {
	def := 10
	if source == "web" {
		def = defaultWebMaxResults
	}
	limit := getMaxResults(requested, def)
	if source == "local_index" && limit > maxLocalIndexResults {
		limit = maxLocalIndexResults
	}
	return limit
}
