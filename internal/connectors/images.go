package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

type ImagesConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

const (
	maxImageSnippetRunes  = 320
	maxImageContextRunes  = 600
	searxngHighlightStart = '\uE000'
	searxngHighlightEnd   = '\uE001'
)

type searxngImageResult struct {
	Title        string      `json:"title"`
	URL          string      `json:"url"`
	Content      string      `json:"content"`
	Source       string      `json:"source"`
	Engine       string      `json:"engine"`
	ImgSrc       string      `json:"img_src"`
	ThumbnailSrc string      `json:"thumbnail_src"`
	ParsedURL    interface{} `json:"parsed_url"`
}

type rankedImageCandidate struct {
	item         searxngImageResult
	imageURL     string
	canonicalURL string
	relevance    int
	index        int
}

func NewImagesConnector(searxngURL string, cacheSvc *cache.Service) *ImagesConnector {
	return &ImagesConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *ImagesConnector) Name() string { return "images" }

func (c *ImagesConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := imageCacheKey(query, maxResults, req)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[images] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults, req)
	if err != nil {
		log.Printf("[images] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"images"},
		Cached:      false,
	}
	recordSearxngError("images", results, err, resp)

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[images] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func imageCacheKey(query string, maxResults int, req *types.SearchRequest) string {
	sources := []string{
		"images", "searxng",
		"max_results=" + strconv.Itoa(maxResults),
		"language=" + req.Language,
		"safe_search=" + strconv.FormatBool(req.SafeSearch),
	}
	return cache.GenerateCacheKey(query, sources, req.TimeRange)
}

func (c *ImagesConnector) searchSearxng(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "images")
	params.Set("engines", "")
	if req.Language != "" {
		params.Set("language", req.Language)
	}
	if req.TimeRange != "" {
		params.Set("time_range", req.TimeRange)
	}
	if req.SafeSearch {
		params.Set("safesearch", "1")
	} else {
		params.Set("safesearch", "0")
	}

	apiURL := c.searxngURL + "/search?" + params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("searxng error: %d", resp.StatusCode)
	}

	var searxngResp struct {
		Results             []searxngImageResult `json:"results"`
		UnresponsiveEngines [][]interface{}      `json:"unresponsive_engines"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searxngResp); err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng decode error: %w", err)
	}

	candidates := make([]rankedImageCandidate, 0, len(searxngResp.Results))
	for i, item := range searxngResp.Results {
		item.Title = normalizeSearxngHighlights(item.Title)
		item.Content = normalizeSearxngHighlights(item.Content)
		imageURL, ok := imageURLFor(item)
		if !ok {
			continue
		}
		canonicalURL, ok := canonicalImageURL(imageURL)
		if !ok {
			continue
		}
		relevance := imageRelevance(query, item)
		candidates = append(candidates, rankedImageCandidate{item: item, imageURL: canonicalURL, canonicalURL: canonicalURL, relevance: relevance, index: i})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].relevance > candidates[j].relevance
	})
	candidates = deduplicateImageCandidates(candidates)

	results := make([]types.SearchResultItem, 0, min(maxResults, len(candidates)))
	for outputIndex, candidate := range candidates {
		if outputIndex >= maxResults {
			break
		}
		engine := candidate.item.Engine
		if engine == "" {
			engine = "searxng"
		}
		results = append(results, types.SearchResultItem{
			Title:        candidate.item.Title,
			URL:          candidate.imageURL,
			Snippet:      imageSnippet(candidate.item.Content, candidate.item.URL, candidate.item.ThumbnailSrc, candidate.imageURL, candidate.item.Source, engine),
			Source:       engine,
			Type:         "image",
			Score:        float64(maxResults - outputIndex),
			CitationID:   imageCitationID(candidate.canonicalURL),
			CanonicalURL: candidate.canonicalURL,
		})
	}

	if len(searxngResp.UnresponsiveEngines) > 0 {
		return results, newSearxngUnresponsiveError(searxngResp.UnresponsiveEngines)
	}

	return results, nil
}

func normalizeSearxngHighlights(text string) string {
	return strings.Map(func(r rune) rune {
		if r == searxngHighlightStart || r == searxngHighlightEnd {
			return -1
		}
		return r
	}, text)
}

func imageURLFor(item searxngImageResult) (string, bool) {
	for _, candidate := range []string{item.ImgSrc, item.ThumbnailSrc} {
		if isHTTPURL(candidate) {
			return candidate, true
		}
	}
	if isImageURL(item.URL) {
		return item.URL, true
	}
	return "", false
}

func isHTTPURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Hostname() != ""
}

func isImageURL(rawURL string) bool {
	if !isHTTPURL(rawURL) {
		return false
	}
	u, _ := url.Parse(rawURL)
	path := strings.ToLower(u.Path)
	for _, extension := range []string{".avif", ".gif", ".jpeg", ".jpg", ".png", ".ppm", ".svg", ".webp"} {
		if strings.HasSuffix(path, extension) {
			return true
		}
	}
	return false
}

func canonicalImageURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !isHTTPURL(rawURL) {
		return "", false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	u.Host = host
	u.Fragment = ""
	u.RawFragment = ""
	return u.String(), true
}

func deduplicateImageCandidates(candidates []rankedImageCandidate) []rankedImageCandidate {
	seen := make(map[string]struct{}, len(candidates))
	deduplicated := candidates[:0]
	for _, candidate := range candidates {
		if _, ok := seen[candidate.canonicalURL]; ok {
			continue
		}
		seen[candidate.canonicalURL] = struct{}{}
		deduplicated = append(deduplicated, candidate)
	}
	return deduplicated
}

func imageRelevance(query string, item searxngImageResult) int {
	terms := imageTerms(query)
	if len(terms) == 0 {
		return 0
	}
	titleMatches := imageTermMatches(terms, item.Title)
	contentMatches := imageTermMatches(terms, boundedImageContent(item.Content))
	return titleMatches*10 + contentMatches
}

func imageTerms(query string) []string {
	words := imageWords(query)
	seen := make(map[string]struct{}, len(words))
	terms := make([]string, 0, len(words))
	for _, word := range words {
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		terms = append(terms, word)
	}
	return terms
}

func imageTermMatches(terms []string, text string) int {
	words := make(map[string]struct{}, len(terms))
	for _, word := range imageWords(text) {
		words[word] = struct{}{}
	}
	matches := 0
	for _, term := range terms {
		if _, ok := words[term]; ok {
			matches++
		}
	}
	return matches
}

func imageWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

func boundedImageContent(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) <= maxImageSnippetRunes {
		return content
	}
	return string(runes[:maxImageSnippetRunes-1]) + "…"
}

func imageSnippet(content, pageURL, thumbnailURL, selectedImageURL, source, engine string) string {
	description := boundedImageContent(content)
	contexts := make([]string, 0, 2)
	if isHTTPURL(pageURL) {
		contexts = append(contexts, "Source page: "+pageURL)
	}
	if previewURL, ok := canonicalImageURL(thumbnailURL); ok && previewURL != selectedImageURL {
		contexts = append(contexts, "Indexed preview: "+previewURL)
	}

	context := boundedImageContext(contexts)
	if context != "" {
		if description == "" {
			return context
		}
		remaining := maxImageContextRunes - len([]rune(context)) - len([]rune(" | "))
		if remaining > 0 {
			return boundedImageRunes(description, remaining) + " | " + context
		}
	}
	if description != "" {
		return description
	}
	return fmt.Sprintf("Source: %s | Engine: %s", source, engine)
}

func boundedImageContext(contexts []string) string {
	context := strings.Join(contexts, " | ")
	if len([]rune(context)) <= maxImageContextRunes {
		return context
	}
	if len(contexts) == 2 && len([]rune(contexts[0])) <= maxImageContextRunes {
		return contexts[0]
	}
	return ""
}

func boundedImageRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func imageCitationID(canonicalURL string) string {
	digest := sha256.Sum256([]byte(canonicalURL))
	return "images:" + hex.EncodeToString(digest[:])
}

func (c *ImagesConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
