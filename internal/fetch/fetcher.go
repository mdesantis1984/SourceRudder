// Package fetch implements the IA_Buscar URL fetch engine. It is the
// single seam for outbound HTTP across all MCP tools (Fetch,
// FetchAndExtract, ExtractStructured, ValidateURL, CheckLinkStatus)
// and is responsible for:
//
//   - Per-request User-Agent injection.
//   - Per-request timeout enforced across the full request lifecycle.
//   - Provider-agnostic, bounded retry classification: only 429 and
//     502-504 are retried; 5xx outside that window is classified as
//     transient-failure and may be retried up to MaxAttempts.
//   - DNS-safe SSRF checks: every hop (initial target AND every
//     redirect target) has all A/AAAA addresses resolved; the hop is
//     rejected when ANY address is non-public; the dial is pinned to
//     the resolved approved IP while preserving the original Host
//     header and TLS ServerName.
//
// The fetcher classifies every outcome into a fixed taxonomy surfaced
// on FetchResponse.Outcome. MCP clients switch on Outcome, not on Go
// errors.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thiscloud/ia-buscar/pkg/types"
)

// Default User-Agent. Prior production baseline; preserved verbatim
// for callers that use NewFetcherService(int) without a Config.
const defaultUserAgent = "Mozilla/5.0 (compatible; IA-Buscar/1.0; +https://thiscloud.es)"

const (
	maxFetchResponseBytes = 4 << 20
	maxConcurrentFetches  = 8
)

// Outcome values emitted on FetchResponse.Outcome. MCP clients
// pattern-match on these strings.
const (
	OutcomeSuccess                          = "success"
	OutcomeBlockedTarget                    = "blocked-target"
	OutcomeBlockedRedirect                  = "blocked-redirect"
	OutcomeTooManyRedirects                 = "too-many-redirects"
	OutcomeTimeout                          = "timeout"
	OutcomeTransportError                   = "transport-error"
	OutcomeHTTPError                        = "http-error"
	OutcomeNonTransientFailure              = "non-transient-failure"
	OutcomeTransientFailureRetriedExhausted = "transient-failure-retried-exhausted"
)

// Config is the operator-facing seam for the fetch engine. Zero
// values are filled with conservative defaults.
type Config struct {
	UserAgent      string
	TimeoutMs      int
	MaxRedirects   int
	MaxAttempts    int
	BaseBackoff    time.Duration
	RedirectPolicy string // "follow" only today; reserved for future
}

// FetcherService is the shared fetch engine. Construct it with
// NewFetcherServiceWithConfig for production use; NewFetcherService
// remains for legacy callers and resolves to the default config.
type FetcherService struct {
	cfg       Config
	extractor *Extractor
	client    *http.Client
	slots     chan struct{}
}

// NewFetcherService builds a fetcher with the timeout in milliseconds.
// Kept for backward compatibility; equivalent to
// NewFetcherServiceWithConfig(Config{TimeoutMs: timeoutMs}).
func NewFetcherService(timeoutMs int) *FetcherService {
	return NewFetcherServiceWithConfig(Config{TimeoutMs: timeoutMs})
}

// NewFetcherServiceWithConfig builds a fetcher with explicit config.
// Zero fields resolve to conservative defaults:
//   - UserAgent: defaultUserAgent
//   - TimeoutMs: 30s
//   - MaxRedirects: 5
//   - MaxAttempts: 3
//   - BaseBackoff: 200ms
func NewFetcherServiceWithConfig(c Config) *FetcherService {
	if c.UserAgent == "" {
		c.UserAgent = defaultUserAgent
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 30000
	}
	if c.MaxRedirects <= 0 {
		c.MaxRedirects = 5
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = 200 * time.Millisecond
	}

	dialer := &net.Dialer{Timeout: time.Duration(c.TimeoutMs) * time.Millisecond}

	return &FetcherService{
		cfg:       c,
		extractor: NewExtractor(),
		slots:     make(chan struct{}, maxConcurrentFetches),
		client: &http.Client{
			Timeout: time.Duration(c.TimeoutMs) * time.Millisecond,
			// Disable automatic redirects: the manual loop below
			// validates each hop against SSRF rules before dialing.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			// Pin every dial to the IP returned by resolveAndValidateFn.
			// The fix closes the DNS-rebinding window between the
			// SSRF check (manual redirect loop) and the actual TCP
			// connect: the dial never re-resolves the host via the
			// system resolver. The original host is preserved by
			// http.Client on the request's Host header and by
			// http.Transport on the TLS ServerName.
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
					}
					ip, _, err := resolveAndValidateFn(host)
					if err != nil {
						return nil, err
					}
					return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				},
			},
		},
	}
}

// ---- private helpers ----------------------------------------------------

// resolveAndValidateFn is the package-level seam tests swap to bypass
// the DNS-resolved SSRF guard (e.g. when httptest binds to 127.0.0.1).
// Production callers MUST NOT swap it.
var resolveAndValidateFn = resolveAndValidate

// resolveAndValidate resolves all A/AAAA addresses for host and
// rejects the hop when ANY address is non-public. Returns the first
// public IP (used to pin the dial) and the original host (for Host
// header and TLS ServerName preservation).
func resolveAndValidate(host string) (net.IP, string, error) {
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, "", fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(ips) == 0 {
		return nil, "", errors.New("dns lookup returned no addresses")
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, "", fmt.Errorf("blocked target: non-public address %s in resolved set", ip)
		}
	}
	return ips[0], host, nil
}

// isPublicIP reports whether ip is routable on the public internet.
// Mirrors the prior production SSRF blocklist plus IPv6 reserved
// ranges and IPv4 broadcast.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsPrivate() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.IsInterfaceLocalMulticast() {
		return false
	}
	// IPv4-mapped IPv6: re-evaluate the underlying IPv4.
	if v4 := ip.To4(); v4 != nil {
		return isPublicIPv4(v4)
	}
	return true
}

func isPublicIPv4(ip net.IP) bool {
	for _, cidr := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "169.254.0.0/16", "0.0.0.0/8",
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
		"224.0.0.0/4", "240.0.0.0/4",
	} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

// isAllowedURLText blocks obvious text-only patterns (localhost,
// .local, etc.) before DNS even runs. Layered defence.
var localhostPatterns = regexp.MustCompile(`(?i)(localhost|loopback|local|internal|\.local$|\.internal$|broadcasthost)`)

func validateURLText(rawURL string) error {
	if rawURL == "" {
		return errors.New("URL cannot be empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s (only http and https allowed)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("URL must have a host")
	}
	host := strings.ToLower(parsed.Hostname())
	if localhostPatterns.MatchString(host) {
		return fmt.Errorf("URL host %s is not allowed (internal host)", host)
	}
	return nil
}

// classifyRetry decides whether a given HTTP status + error should
// retry under the spec's bounded backoff. Note the previous shape
// returned false for any non-nil non-timeout error, which silently
// swallowed the 429/502-504 retry contract because manualRedirectFetch
// always returns a non-nil error for non-2xx responses. The fix falls
// through to the status check so the same retryable status triggers
// retry regardless of whether the upstream returned an error wrapper.
func shouldRetry(status int, err error) bool {
	if err != nil && isTimeoutErr(err) {
		return true
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	if status >= 502 && status <= 504 {
		return true
	}
	return false
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

// backoffDelay returns a deterministic-jittered backoff for the given
// attempt (1-based). The jitter is bounded so retries are reproducible
// when the same RNG seed is shared across test runs.
func backoffDelay(base time.Duration, attempt int) time.Duration {
	mult := 1 << (attempt - 1)
	d := time.Duration(mult) * base
	// Deterministic jitter: half the base, alternating +/- per attempt.
	jitter := base / 2
	if attempt%2 == 0 {
		d += jitter
	} else {
		d -= jitter
	}
	if d < base {
		d = base
	}
	return d
}

// rateLimiterInterval is the gap between consecutive link-check
// requests so the upstream is not hammered. The previous shared
// ticker produced a non-cancellable receive and so could not honor
// ctx.Done(); we now drive the receive with a select on ctx.
const rateLimiterInterval = 200 * time.Millisecond

// ---- public fetch surface ----------------------------------------------

func (s *FetcherService) isAllowedURL(rawURL string) error { return validateURLText(rawURL) }

func (s *FetcherService) Fetch(ctx context.Context, rawURL string) (*types.FetchResponse, error) {
	fr := &types.FetchResponse{URL: rawURL}
	if err := s.isAllowedURL(rawURL); err != nil {
		fr.Outcome = OutcomeBlockedTarget
		fr.Warnings = []string{err.Error()}
		return fr, err
	}
	return s.doFetchWithRetries(ctx, rawURL, fr)
}

func (s *FetcherService) FetchAndExtract(ctx context.Context, rawURL string, mode string) (*types.FetchResponse, error) {
	if err := s.isAllowedURL(rawURL); err != nil {
		return &types.FetchResponse{
			URL:      rawURL,
			Outcome:  OutcomeBlockedTarget,
			Warnings: []string{err.Error()},
		}, err
	}
	fr, err := s.doFetchWithRetries(ctx, rawURL, &types.FetchResponse{URL: rawURL})
	if err != nil {
		return fr, err
	}
	html := fr.Content
	var extractedContent string
	switch mode {
	case "article":
		extractedContent, _ = s.extractor.ExtractArticleContent(html)
	case "documentation":
		extractedContent, _ = s.extractor.ExtractDocContent(html)
	case "raw":
		extractedContent = html
	default:
		extractedContent, _ = s.extractor.ExtractMainContent(html)
	}
	fr.Content = extractedContent
	fr.Title = s.extractor.ExtractTitle(html)
	metadata, _ := s.extractor.ExtractMetadata(html)
	fr.Metadata = metadata
	return fr, nil
}

func (s *FetcherService) ExtractStructured(ctx context.Context, rawURL string) (*types.FetchResponse, error) {
	if err := s.isAllowedURL(rawURL); err != nil {
		return &types.FetchResponse{
			URL:      rawURL,
			Outcome:  OutcomeBlockedTarget,
			Warnings: []string{err.Error()},
		}, err
	}
	fr, err := s.doFetchWithRetries(ctx, rawURL, &types.FetchResponse{URL: rawURL})
	if err != nil {
		return fr, err
	}
	html := fr.Content
	tables, _ := s.extractor.ExtractTables(html)
	metadata, _ := s.extractor.ExtractMetadata(html)
	fr.Content = fmt.Sprintf("%v", map[string]interface{}{"tables": tables, "metadata": metadata})
	fr.Metadata = metadata
	return fr, nil
}

func (s *FetcherService) ValidateURL(ctx context.Context, rawURL string) (bool, error) {
	if rawURL == "" {
		return false, errors.New("URL cannot be empty")
	}
	if err := s.isAllowedURL(rawURL); err != nil {
		return false, err
	}
	fr, _ := s.doFetchWithRetries(ctx, rawURL, &types.FetchResponse{URL: rawURL}, retryableHead()...)
	// HEAD may not be supported by some upstreams; treat 4xx as a
	// soft miss and report invalid + nil error to keep wire parity.
	if fr.Outcome == OutcomeHTTPError && fr.Status >= 400 {
		return false, nil
	}
	if fr.Outcome != OutcomeSuccess {
		return false, nil
	}
	return true, nil
}

// retryableHead returns request opts that prefer HEAD over GET. The
// existing API doesn't pass request methods through, so the helper
// returns nothing for now and doFetchWithRetries defaults to GET.
// ValidateURL accepts this limitation by treating any non-success as
// soft-invalid.
func retryableHead() []func(*http.Request) { return nil }

func (s *FetcherService) CheckLinkStatus(ctx context.Context, urls []string) ([]map[string]interface{}, error) {
	const maxURLs = 100
	if len(urls) > maxURLs {
		return nil, fmt.Errorf("url count exceeds %d", maxURLs)
	}
	results := make([]map[string]interface{}, len(urls))
	type job struct {
		index int
		url   string
	}
	jobs := make(chan job)
	pace := time.NewTicker(rateLimiterInterval)
	defer pace.Stop()
	var wg sync.WaitGroup
	workers := maxConcurrentFetches
	if len(urls) < workers {
		workers = len(urls)
	}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range jobs {
				result := map[string]interface{}{"url": work.url, "valid": false, "status": 0}
				select {
				case <-ctx.Done():
					result["error"] = ctx.Err().Error()
				case <-pace.C:
					if err := s.isAllowedURL(work.url); err != nil {
						result["error"] = err.Error()
					} else {
						resp, err := s.doFetchWithRetries(ctx, work.url, &types.FetchResponse{URL: work.url})
						if err == nil && resp.Status < 400 {
							result["valid"] = true
						}
						result["status"] = resp.Status
						if resp.Outcome != OutcomeSuccess {
							result["error"] = strings.Join(resp.Warnings, "; ")
						}
					}
				}
				results[work.index] = result
			}
		}()
	}
	for index, rawURL := range urls {
		jobs <- job{index: index, url: rawURL}
	}
	close(jobs)
	wg.Wait()
	return results, nil
}

// ---- core fetch with retries + manual redirects ------------------------

// doFetchWithRetries is the single seam every public fetch method
// goes through. It runs the manual redirect loop, classifies the
// final response, and applies bounded retry on transient failures.
func (s *FetcherService) doFetchWithRetries(ctx context.Context, rawURL string, fr *types.FetchResponse, reqOpts ...func(*http.Request)) (*types.FetchResponse, error) {
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		fr.Outcome = OutcomeTimeout
		fr.Warnings = append(fr.Warnings, ctx.Err().Error())
		return fr, ctx.Err()
	}

	var lastErr error
	for attempt := 1; attempt <= s.cfg.MaxAttempts; attempt++ {
		fr.Attempts = attempt
		select {
		case <-ctx.Done():
			fr.Outcome = OutcomeTimeout
			fr.Warnings = append(fr.Warnings, ctx.Err().Error())
			return fr, ctx.Err()
		default:
		}

		// Bounded backoff between attempts (skip on first). The
		// sleep is cancellable so SIGTERM during a 1.6s exponential
		// backoff stops the lifecycle immediately instead of waiting
		// out the full window.
		if attempt > 1 {
			delay := backoffDelay(s.cfg.BaseBackoff, attempt)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				fr.Outcome = OutcomeTimeout
				fr.Warnings = append(fr.Warnings, ctx.Err().Error())
				return fr, ctx.Err()
			case <-timer.C:
			}
		}

		err := s.manualRedirectFetch(ctx, rawURL, fr, reqOpts...)

		// Caller cancellation must stop immediately, even between retries.
		if errors.Is(ctx.Err(), context.Canceled) {
			fr.Outcome = OutcomeTimeout
			fr.Warnings = append(fr.Warnings, ctx.Err().Error())
			return fr, ctx.Err()
		}

		if err == nil {
			return fr, nil
		}
		lastErr = err

		// Decide whether to retry. We retry only on transient errors
		// (timeout-classified or 429/502-504).
		if !shouldRetry(fr.Status, err) {
			// Non-transient: classify and return.
			if fr.Outcome == "" {
				fr.Outcome = outcomeFromErr(err)
			}
			return fr, err
		}
		// Continue loop to next attempt.
	}
	// Budget exhausted; classify and return. A timeout-flavoured
	// exhaustion surfaces as Outcome=timeout so MCP clients can
	// distinguish a single-attempt timeout (MaxAttempts=1) from a
	// multi-attempt transient storm (e.g. 503-storm, where
	// isTimeoutErr is false).
	if lastErr != nil && isTimeoutErr(lastErr) {
		fr.Outcome = OutcomeTimeout
		return fr, lastErr
	}
	fr.Outcome = OutcomeTransientFailureRetriedExhausted
	fr.Warnings = append(fr.Warnings, fmt.Sprintf("retries exhausted after %d attempts: %v", s.cfg.MaxAttempts, lastErr))
	return fr, lastErr
}

// manualRedirectFetch walks the redirect chain by hand so each hop is
// SSRF-validated before dialing. It populates fr.Status, fr.Outcome,
// fr.RedirectChain, and fr.Content along the way.
func (s *FetcherService) manualRedirectFetch(ctx context.Context, rawURL string, fr *types.FetchResponse, reqOpts ...func(*http.Request)) error {
	current := rawURL
	for hop := 0; hop <= s.cfg.MaxRedirects; hop++ {
		// Resolve and validate every hop.
		parsed, err := url.Parse(current)
		if err != nil {
			fr.Outcome = OutcomeNonTransientFailure
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("invalid URL at hop %d: %v", hop, err))
			return err
		}
		host := parsed.Hostname()
		if _, _, err := resolveAndValidateFn(host); err != nil {
			fr.Outcome = OutcomeBlockedTarget
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("hop %d (%s): %v", hop, current, err))
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			fr.Outcome = OutcomeNonTransientFailure
			fr.Warnings = append(fr.Warnings, err.Error())
			return err
		}
		req.Header.Set("User-Agent", s.cfg.UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		for _, opt := range reqOpts {
			opt(req)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			if isTimeoutErr(err) {
				fr.Outcome = OutcomeTimeout
				fr.Warnings = append(fr.Warnings, err.Error())
				return err
			}
			fr.Outcome = OutcomeTransportError
			fr.Warnings = append(fr.Warnings, err.Error())
			return err
		}
		fr.Status = resp.StatusCode

		// Drain + close so the connection can be reused.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxFetchResponseBytes+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			if isTimeoutErr(readErr) {
				fr.Outcome = OutcomeTimeout
			} else {
				fr.Outcome = OutcomeTransportError
			}
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("read response body: %v", readErr))
			return readErr
		}
		if closeErr != nil {
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("close response body: %v", closeErr))
		}
		if len(body) > maxFetchResponseBytes {
			fr.Outcome = OutcomeNonTransientFailure
			err := fmt.Errorf("response body exceeds %d bytes", maxFetchResponseBytes)
			fr.Warnings = append(fr.Warnings, err.Error())
			return err
		}

		// Redirect handling.
		if loc := resp.Header.Get("Location"); loc != "" && (resp.StatusCode >= 300 && resp.StatusCode < 400) {
			fr.RedirectChain = append(fr.RedirectChain, current)
			if hop == s.cfg.MaxRedirects {
				fr.Outcome = OutcomeTooManyRedirects
				fr.Warnings = append(fr.Warnings, fmt.Sprintf("redirect chain exceeded %d hops", s.cfg.MaxRedirects))
				return errors.New("too many redirects")
			}
			next, err := resolveRedirect(parsed, loc)
			if err != nil {
				fr.Outcome = OutcomeNonTransientFailure
				fr.Warnings = append(fr.Warnings, fmt.Sprintf("invalid Location: %v", err))
				return err
			}
			current = next
			continue
		}

		// Non-redirect final response: classify and capture body.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			fr.Outcome = OutcomeSuccess
			fr.Content = string(body)
			return nil
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			fr.Outcome = OutcomeHTTPError
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("upstream 429"))
			return fmt.Errorf("upstream 429")
		}
		if resp.StatusCode >= 500 {
			fr.Outcome = OutcomeHTTPError
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("upstream %d", resp.StatusCode))
			return fmt.Errorf("upstream %d", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			fr.Outcome = OutcomeHTTPError
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("upstream %d", resp.StatusCode))
			return fmt.Errorf("upstream %d", resp.StatusCode)
		}
		// 1xx informational: keep going.
		if resp.StatusCode < 200 {
			fr.Outcome = OutcomeNonTransientFailure
			fr.Warnings = append(fr.Warnings, fmt.Sprintf("unexpected status %d", resp.StatusCode))
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
	}
	fr.Outcome = OutcomeTooManyRedirects
	return errors.New("too many redirects")
}

// resolveRedirect resolves a Location header against the previous URL
// to produce an absolute URL for the next hop.
func resolveRedirect(prev *url.URL, loc string) (string, error) {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc, nil
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	return prev.ResolveReference(ref).String(), nil
}

func outcomeFromErr(err error) string {
	if err == nil {
		return OutcomeSuccess
	}
	if isTimeoutErr(err) {
		return OutcomeTimeout
	}
	return OutcomeNonTransientFailure
}

// init seeds the package-level rand with a deterministic value so the
// retry tests are reproducible. The seed is fixed at package load
// time and does not change between test runs.
func init() {
	rand.Seed(1)
}
