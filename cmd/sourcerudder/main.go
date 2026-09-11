package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/mcp"
	"github.com/mdesantis1984/SourceRudder/internal/memory"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

var (
	transport      = flag.String("transport", "stdio", "Transport mode: stdio or http")
	httpAddr       = flag.String("http-addr", ":8080", "HTTP server address")
	searxngURL     = flag.String("searxng-url", envDefault("SEARXNG_URL", "http://localhost:8888"), "SearXNG URL (env: SEARXNG_URL)")
	cacheTTL       = flag.Int("cache-ttl", 300, "In-process cache TTL in seconds")
	fetchTimeoutMs = flag.Int("fetch-timeout-ms", 30000, "Fetch timeout in milliseconds")
	authKey        = flag.String("auth-key", "", "API key for authentication (optional)")
	// Deprecated no-op compatibility flags. Reddit search is now served only by
	// the configured SearXNG instance and never calls Reddit's API directly.
	redditUserAgent = flag.String("reddit-user-agent", envDefault("REDDIT_USER_AGENT", ""), "Deprecated no-op; search_reddit uses SearXNG")
	redditBaseURL   = flag.String("reddit-base-url", envDefault("REDDIT_BASE_URL", ""), "Deprecated no-op; search_reddit uses SearXNG")
	memoryURL       = flag.String("memory-url", "", "IA_Recuerdo (memory) base URL; when empty, the integration is disabled and Save is a no-op (env: MEMORY_URL)")
	memoryAPIKey    = flag.String("memory-apikey", "", "IA_Recuerdo (memory) bearer key; travels as Authorization: Bearer (env: MEMORY_APIKEY)")
	localIndexPath  = flag.String("local-index-path", "", "Read-only local index corpus path (env: LOCAL_INDEX_PATH)")
)

// envDefault returns the value of the named environment variable when
// it is set and non-empty, otherwise the fallback. It lets operators
// configure the Reddit connector through environment variables without
// changing the command line, following the same env-flag convention
// used elsewhere in SourceRudder's existing flags.
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
// deploy/systemd/sourcerudder.service so a misconfigured pod still boots.
func buildFetchConfig(flagTimeoutMs int) fetch.Config {
	timeoutMs := envInt("FETCH_TIMEOUT_MS", 30000)
	if flagTimeoutMs > 0 {
		timeoutMs = flagTimeoutMs
	}
	return fetch.Config{
		UserAgent:    envDefault("FETCH_USER_AGENT", "SourceRudder/2.0.0 (anonymous-only)"),
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

func resolveLocalIndexPath(flagValue string) string {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path
	}
	return strings.TrimSpace(envDefault("LOCAL_INDEX_PATH", ""))
}

// authKeyEnvVar is the environment variable read when the operator
// does not pass -auth-key on the command line. Promoting the value
// out of an env var keeps the secret out of argv (ps aux / process
// listings and shell history.
const authKeyEnvVar = "SOURCERUDDER_AUTH_KEY"

// resolveAuthKey layers the SOURCERUDDER_AUTH_KEY env var under the
// explicit -auth-key flag value. The explicit flag wins when both
// are set so an operator's CLI override is never silently dropped.
//
// The flag value is preserved verbatim (no trimming) so existing
// scripts that pass a literal key keep working — a trailing newline
// inside the flag is the operator's choice. The env value IS trimmed
// because secrets files routinely carry trailing newlines and shell
// exports routinely carry leading whitespace; a key that fails every
// validator comparison because of a stray byte is a silent outage.
//
// When both inputs are empty (or whitespace-only on the env side)
// the helper returns an empty string so auth.NewValidator produces
// the documented fail-closed validator — the no-bypass contract
// already in internal/auth/auth.go.
func resolveAuthKey(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	raw, ok := os.LookupEnv(authKeyEnvVar)
	if !ok {
		return ""
	}
	return strings.TrimSpace(raw)
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== SourceRudder Starting ===")
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
	observability.InitTracing("sourcerudder")
	authValidator := auth.NewValidator(resolveAuthKey(*authKey))

	memURL, memKey := resolveMemoryConfig(*memoryURL, *memoryAPIKey)
	memClient := memory.NewClient(memURL, memKey)
	history := cache.NewHistoryService(100)

	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	webConnector := connectors.NewWebConnector(*searxngURL, cacheSvc)
	cm.Register(webConnector)
	cm.Register(connectors.NewOfficialDocsConnector(webConnector))
	cm.Register(connectors.NewGitHubConnector("", cacheSvc))
	cm.Register(connectors.NewStackOverflowConnector(cacheSvc))
	cm.Register(connectors.NewNPMConnector(cacheSvc))
	cm.Register(connectors.NewNuGetConnector(cacheSvc))
	cm.Register(connectors.NewPyPIConnector(cacheSvc))
	cm.Register(connectors.NewDockerHubConnector(cacheSvc))
	cm.Register(connectors.NewAcademicConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{SearxngURL: *searxngURL}, cacheSvc))
	cm.Register(connectors.NewYouTubeConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewImagesConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewNewsConnector(*searxngURL, cacheSvc))
	if path := resolveLocalIndexPath(*localIndexPath); path != "" {
		localIndex, err := connectors.NewLocalIndexConnector(path)
		if err != nil {
			log.Fatalf("Failed to load local index: %v", err)
		}
		cm.Register(localIndex)
	}

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
	if *transport == "stdio" {
		log.Printf("SourceRudder running with %s transport", trans.Name())
		if err := trans.Start(ctx); err != nil {
			log.Fatalf("Failed to start transport: %v", err)
		}
		return
	}
	if err := trans.Start(ctx); err != nil {
		log.Fatalf("Failed to start transport: %v", err)
	}
	log.Printf("SourceRudder running with %s transport", trans.Name())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutdown signal received")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := trans.Stop(shutdownCtx); err != nil {
		log.Printf("Transport shutdown error: %v", err)
	}
	log.Println("=== SourceRudder Stopped ===")
}
