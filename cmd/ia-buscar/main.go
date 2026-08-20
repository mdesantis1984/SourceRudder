package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thiscloud/ia-buscar/internal/auth"
	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/mcp"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

var (
	transport          = flag.String("transport", "stdio", "Transport mode: stdio or http")
	httpAddr           = flag.String("http-addr", ":8080", "HTTP server address")
	searxngURL         = flag.String("searxng-url", "http://10.0.0.201:8080", "SearxNG URL")
	cacheTTL           = flag.Int("cache-ttl", 300, "In-process cache TTL in seconds")
	fetchTimeoutMs     = flag.Int("fetch-timeout-ms", 30000, "Fetch timeout in milliseconds")
	authKey            = flag.String("auth-key", "", "API key for authentication (optional)")
	redditUserAgent    = flag.String("reddit-user-agent", envDefault("REDDIT_USER_AGENT", "ia-buscar/1.0 (by /r/ThisCloudServices)"), "User-Agent header for Reddit requests (Reddit requires a unique, descriptive UA)")
	redditClientID     = flag.String("reddit-client-id", envDefault("REDDIT_CLIENT_ID", ""), "Reddit OAuth client ID; empty disables OAuth")
	redditClientSecret = flag.String("reddit-client-secret", envDefault("REDDIT_CLIENT_SECRET", ""), "Reddit OAuth client secret; empty disables OAuth")
	redditBaseURL      = flag.String("reddit-base-url", envDefault("REDDIT_BASE_URL", "https://www.reddit.com"), "Reddit base URL; switch to https://oauth.reddit.com when using OAuth")
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

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== IA_Buscar Starting ===")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cacheSvc := cache.NewService(*cacheTTL)
	fetchSvc := fetch.NewFetcherService(30000)
	synthSvc := synthesis.NewService()
	// One Metrics instance is wired into both surfaces: the package-level
	// default (so connectors that call observability.Default().Record... hit
	// it) and the MCP server (so /metrics serves the same registry). Two
	// independent registries would silently drop every counter on the floor.
	met := observability.New()
	observability.SetDefault(met)
	observability.InitTracing("ia-buscar")
	authValidator := auth.NewValidator(*authKey)

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
		BaseURL:      *redditBaseURL,
		UserAgent:    *redditUserAgent,
		ClientID:     *redditClientID,
		ClientSecret: *redditClientSecret,
	}, cacheSvc))
	cm.Register(connectors.NewYouTubeConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewImagesConnector(*searxngURL, cacheSvc))
	cm.Register(connectors.NewNewsConnector(*searxngURL, cacheSvc))

	server := mcp.NewServer(cm, planner, *transport, *httpAddr, *searxngURL, *cacheTTL, *fetchTimeoutMs, fetchSvc, synthSvc, authValidator, met)
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
