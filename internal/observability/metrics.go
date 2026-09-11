package observability

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	mu             sync.RWMutex
	toolsCalls     map[string]int64
	sessionsActive int64
	searchesTotal  int64
	errorsTotal    int64
	registry       *prometheus.Registry
	httpRequests   *prometheus.CounterVec
	searchLatency  *prometheus.HistogramVec
	searchDegraded *prometheus.CounterVec
}

// defaultMetrics is the package-level Metrics instance used by connectors
// and other packages that don't hold a direct reference. main() wires it
// once at startup; tests that need isolation can call SetDefault.
var (
	defaultMu      sync.RWMutex
	defaultMetrics *Metrics
)

// SetDefault installs m as the package-level Metrics instance. It is
// intended to be called once during boot. Subsequent calls overwrite the
// previous instance.
func SetDefault(m *Metrics) {
	defaultMu.Lock()
	defaultMetrics = m
	defaultMu.Unlock()
}

// Default returns the package-level Metrics instance, creating one on
// first use so connectors can call RecordSearchDegraded from anywhere
// without holding a direct reference. The instance is shared across the
// process and is safe for concurrent use.
func Default() *Metrics {
	defaultMu.RLock()
	m := defaultMetrics
	defaultMu.RUnlock()
	if m != nil {
		return m
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultMetrics == nil {
		defaultMetrics = New()
	}
	return defaultMetrics
}

func New() *Metrics {
	m := &Metrics{
		toolsCalls: make(map[string]int64),
		registry:   prometheus.NewRegistry(),
	}
	m.registry.MustRegister(prometheus.NewGoCollector())
	m.registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	m.httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sourcerudder_http_requests_total",
			Help: "Total HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	m.searchLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sourcerudder_search_latency_seconds",
			Help:    "Search latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"source"},
	)
	m.searchDegraded = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sourcerudder_search_degraded_total",
			Help: "Number of search responses that surfaced an upstream degradation (e.g. SearxNG engines not responding, Reddit 429/5xx). The 'kind' label names the failure mode.",
		},
		[]string{"source", "kind"},
	)
	m.registry.MustRegister(m.httpRequests)
	m.registry.MustRegister(m.searchLatency)
	m.registry.MustRegister(m.searchDegraded)
	return m
}

func (m *Metrics) IncrToolCall(toolName string) {
	m.mu.Lock()
	m.toolsCalls[toolName]++
	m.mu.Unlock()
}

func (m *Metrics) IncrSession() {
	atomic.AddInt64(&m.sessionsActive, 1)
}

func (m *Metrics) IncrSearch(source string) {
	atomic.AddInt64(&m.searchesTotal, 1)
}

func (m *Metrics) IncrError() {
	atomic.AddInt64(&m.errorsTotal, 1)
}

func (m *Metrics) SetSkillsLoaded(int64) {}

func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
		h.ServeHTTP(w, r)
	}
}

// Registry exposes the underlying Prometheus registry so tests in other
// packages can read counter values via prometheus.Registry.Gather()
// without having to spin up an HTTP scrape. Production callers should
// use Handler() instead.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

func (m *Metrics) JSON() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data := map[string]interface{}{
		"toolsCalls":     m.toolsCalls,
		"sessionsActive": atomic.LoadInt64(&m.sessionsActive),
		"searchesTotal":  atomic.LoadInt64(&m.searchesTotal),
		"errorsTotal":    atomic.LoadInt64(&m.errorsTotal),
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return string(b)
}

func (m *Metrics) RecordSearchLatency(source string, seconds float64) {
	m.searchLatency.WithLabelValues(source).Observe(seconds)
}

func (m *Metrics) RecordHTTPRequest(method, path, status string) {
	m.httpRequests.WithLabelValues(method, path, status).Inc()
}

// RecordSearchDegraded increments sourcerudder_search_degraded_total whenever a
// connector returns a response that signals upstream degradation (SearxNG
// engines not responding, Reddit 429/5xx, etc.). The 'source' label is the
// connector name; the 'kind' label is a stable, lowercase, snake_case tag
// naming the failure mode (e.g. "unresponsive_engines", "rate_limited",
// "transport", "upstream_http_5xx"). This metric is for observability
// only — SourceRudder does not attempt to repair external engines.
func (m *Metrics) RecordSearchDegraded(source, kind string) {
	if source == "" {
		source = "unknown"
	}
	if kind == "" {
		kind = "unknown"
	}
	m.searchDegraded.WithLabelValues(source, kind).Inc()
}
