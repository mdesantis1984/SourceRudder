package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/thiscloud/ia-buscar/internal/auth"
	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/mcp"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

var (
	transport       = flag.String("transport", "stdio", "Transport mode: stdio or http")
	httpAddr        = flag.String("http-addr", ":8080", "HTTP server address")
	searxngURL      = flag.String("searxng-url", "http://10.0.0.201:8080", "SearxNG URL")
	cacheTTL        = flag.Int("cache-ttl", 300, "In-process cache TTL in seconds")
	fetchTimeoutMs  = flag.Int("fetch-timeout-ms", 30000, "Fetch timeout in milliseconds")
	authKey         = flag.String("auth-key", "", "API key for authentication (optional)")
	redditUserAgent = flag.String("reddit-user-agent", envDefault("REDDIT_USER_AGENT", "ia-buscar/1.2 (anonymous-only)"), "User-Agent header for Reddit requests; deployment-specific UA is required by Reddit's anonymous API contract")
	redditBaseURL   = flag.String("reddit-base-url", envDefault("REDDIT_BASE_URL", "https://www.reddit.com"), "Reddit base URL; always anonymous against the public www.reddit.com JSON endpoint")
	memoryURL       = flag.String("memory-url", "", "IA_Recuerdo (memory) base URL; when empty, the integration is disabled and Save is a no-op (env: MEMORY_URL)")
	memoryAPIKey    = flag.String("memory-apikey", "", "IA_Recuerdo (memory) bearer key; travels as Authorization: Bearer (env: MEMORY_APIKEY)")
)

// envDefault returns the value of the named environment variable when
// it is set and non-empty, otherwise the fallback. It lets operators
// configure the Reddit connector through environment variables without
// changing the command line, following the same env-flag convention
// used elsewhere in IA_Buscar's existing flags.
func envDefault(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return fallback
}

// envInt returns the int value of the named environment variable when
// it is set, non-empty, and parses cleanly; otherwise the fallback.
// Operators configure FETCH_TIMEOUT_MS / FETCH_MAX_REDIRECTS /
// FETCH_MAX_ATTEMPTS through the deployment manifests; this helper
// closes the gap between the manifests and the FetcherService config.
func envInt(name string, fallback int) int {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

// buildFetchConfig returns the FetcherService config, layering the
// deployment-provided FETCH_* env vars under the flag values. The
// flag values win when both are set so an operator's CLI override
// always takes precedence. The defaults match the values already
// documented in deploy/kubernetes/deployment.yaml and
// deploy/systemd/ia-buscar.service so a misconfigured pod still boots.
func buildFetchConfig(flagTimeoutMs int) fetch.Config {
	timeoutMs := envInt("FETCH_TIMEOUT_MS", 30000)
	if flagTimeoutMs > 0 {
		timeoutMs = flagTimeoutMs
	}
	return fetch.Config{
		UserAgent:    envDefault("FETCH_USER_AGENT", "ia-buscar/1.2 (anonymous-only)"),
		TimeoutMs:    timeoutMs,
		MaxRedirects: envInt("FETCH_MAX_REDIRECTS", 5),
		MaxAttempts:  envInt("FETCH_MAX_ATTEMPTS", 3),
		BaseBackoff:  200 * time.Millisecond,
	}
}

// resolveMemoryConfig layers the MEMORY_URL / MEMORY_APIKEY env vars
// under the explicit -memory-url / -memory-apikey flag values. The
// explicit flags win when both are set so an operator's CLI override
// is never silently dropped. It is split out from main so the
// precedence rule is unit-tested without re-declaring flags.
func resolveMemoryConfig(flagURL, flagKey string) (string, string) {
	url := flagURL
	if url == "" {
		url = envDefault("MEMORY_URL", "")
	}
	key := flagKey
	if key == "" {
		key = envDefault("MEMORY_APIKEY", "")
	}
	return url, key
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== IA_Buscar Starting ===")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cacheSvc := cache.NewService(*cacheTTL)
	fetchSvc := fetch.NewFetcherServiceWithConfig(buildFetchConfig(*fetchTimeoutMs))
	synthSvc := synthesis.NewService()
	// One Metrics instance is wired into both surfaces: the package-level
	// default (so connectors that call observability.Default().Record... hit
	// it) and the MCP server (so /metrics serves the same registry). Two
	// independent registries would silently drop every counter on the floor.
	met := observability.New()
	observability.SetDefault(met)
	observability.InitTracing("ia-buscar")
	authValidator := auth.NewValidator(*authKey)

	memURL, memKey := resolveMemoryConfig(*memoryURL, *memoryAPIKey)
	memClient := memory.NewClient(memURL, memKey)
	history := cache.NewHistoryService(100)

	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	cm.Register(connectors.NewWebConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewGitHubConnector("", cacheSvc))
	cm.Register(connectors.NewStackOverflowConnector(cacheSvc))
	cm.Register(connectors.NewNPMConnector(cacheSvc))
	cm.Register(connectors.NewNuGetConnector(cacheSvc))
	cm.Register(connectors.NewPyPIConnector(cacheSvc))
	cm.Register(connectors.NewDockerHubConnector(cacheSvc))
	cm.Register(connectors.NewAcademicConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{
		BaseURL:   *redditBaseURL,
		UserAgent: *redditUserAgent,
	}, cacheSvc))
	cm.Register(connectors.NewYouTubeConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewImagesConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewNewsConnector(*searxngURL, cacheSvc))

	server := mcp.NewServer(cm, planner, *transport, *httpAddr, *searxngURL, *cacheTTL, *fetchTimeoutMs, fetchSvc, synthSvc, authValidator, met, history, memClient)
	var trans mcp.Transport
	switch *transport {
	case "stdio":
		trans = mcp.NewSTDIOTransport(server)
	case "http":
		trans = mcp.NewHTTPTransport(*httpAddr, server)
	default:
		log.Fatalf("Unknown transport mode: %s", *transport)
	}
	if err := trans.Start(ctx); err != nil {
		log.Fatalf("Failed to start transport: %v", err)
	}
	log.Printf("IA_Buscar running with %s transport", trans.Name())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutdown signal received")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := trans.Stop(shutdownCtx); err != nil {
		log.Printf("Transport shutdown error: %v", err)
	}
	log.Println("=== IA_Buscar Stopped ===")
}
