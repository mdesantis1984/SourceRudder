package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

const (
	serverName    = "sourcerudder"
	serverVersion = "3.0.0"
)

const maxRPCRequestBytes = 1 << 20

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 2 * time.Minute
	httpIdleTimeout       = 2 * time.Minute
)

type Server struct {
	transport         string
	httpAddr          string
	searxngURL        string
	cacheTTL          int
	toolsRegistry     []Tool
	resourcesRegistry []Resource
	connectorManager  *search.ConnectorManager
	planner           *search.Planner
	met               *observability.Metrics
	fetcherService    *fetch.FetcherService
	synthesisService  *synthesis.Service
	authValidator     *auth.Validator
	// history is shared by search handlers and get_search_history.
	// Production construction supplies it; nil remains valid for
	// isolated test fixtures.
	history *cache.HistoryService
	// httpSrv is the live HTTP server. It is set by Start (when
	// transport == "http") and closed by Stop via Shutdown. Outside
	// of HTTP transport it stays nil.
	httpSrv *http.Server
	// httpLn is the listening socket the httpSrv is bound to. It is
	// stored so Stop can be invoked even after the server has been
	// started in a goroutine.
	httpLn net.Listener
}

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Handler     func(ctx context.Context, args json.RawMessage) (interface{}, error)
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricsResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *metricsResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// NewServer wires an MCP server. met MUST be the same *observability.Metrics
// instance that main wires into observability.SetDefault so the counters
// connectors increment (e.g. sourcerudder_search_degraded_total) are the
// same ones the /metrics endpoint serves. Passing nil is a programming
// error: the Server has no metrics surface, /metrics will panic on
// scrape, and a future regression could re-introduce the production
// split where /metrics was empty while connectors still ticked counters.
func NewServer(connectorManager *search.ConnectorManager, planner *search.Planner, transport, httpAddr, searxngURL string, cacheTTL int, fetchTimeoutMs int, fetchSvc *fetch.FetcherService, synthSvc *synthesis.Service, authValidator *auth.Validator, met *observability.Metrics, history *cache.HistoryService) *Server {
	_ = fetchTimeoutMs
	if met == nil {
		// Fail loud, not silent: a nil metrics here is the exact
		// shape of the bug this Server was hardened against.
		panic("mcp.NewServer: met is required; the same *observability.Metrics wired into observability.SetDefault must be passed here so /metrics reflects what connectors increment")
	}
	s := &Server{
		transport:        transport,
		httpAddr:         httpAddr,
		searxngURL:       searxngURL,
		cacheTTL:         cacheTTL,
		connectorManager: connectorManager,
		planner:          planner,
		met:              met,
		fetcherService:   fetchSvc,
		synthesisService: synthSvc,
		authValidator:    authValidator,
		history:          history,
	}
	s.buildToolsRegistry()
	s.registerTools()
	s.buildResourcesRegistry()
	return s
}

// Handler returns the production HTTP handler chain for this Server:
// /healthz is always open so Kubernetes liveness/readiness probes
// can hit it without credentials, while /mcp and /metrics stay
// wrapped by the auth middleware when one is configured. When the
// operator passes a nil validator the protected routes still get a
// rejecting fallback so /mcp and /metrics never become silently
// open in production. It is the single source of truth for the
// wire surface so HTTPTransport.Start and end-to-end tests drive
// the same boundary instead of two diverging copies.
func (s *Server) Handler() http.Handler {
	// Resolve the effective validator: a non-nil configured
	// validator wins; otherwise install the rejecting fallback so
	// the protected routes stay closed when the operator forgot to
	// wire a key.
	var validator *auth.Validator
	if s.authValidator != nil {
		validator = s.authValidator
	} else {
		validator = auth.NewValidator("")
	}

	// Sub-mux for the protected routes. /healthz is registered on
	// the outer root mux so the probe path bypasses auth entirely.
	protected := http.NewServeMux()
	protected.HandleFunc("/mcp", s.HandleHTTP)
	protected.HandleFunc("/metrics", s.met.Handler())
	protectedHandler := validator.Middleware(protected)

	root := http.NewServeMux()
	root.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
	})
	// Register the same wrapped protected handler for both
	// /mcp and /metrics: ServeMux dispatches by path INSIDE the
	// wrapped handler, so /metrics reaches s.met.Handler() and
	// /mcp reaches s.HandleHTTP even though they share the
	// validator's middleware instance.
	root.Handle("/mcp", protectedHandler)
	root.Handle("/metrics", protectedHandler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &metricsResponseWriter{ResponseWriter: w}
		root.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		s.met.RecordHTTPRequest(r.Method, r.URL.Path, fmt.Sprintf("%d", status))
	})
}

func (s *Server) buildToolsRegistry() {
	s.toolsRegistry = []Tool{
		{Name: "search_web", Description: "Búsqueda web amplia. Backend: SearxNG. Devuelve strategy=\"searxng\".", InputSchema: searchInputSchema()},
		{Name: "search_news", Description: "Noticias y actualidad. Backend: SearxNG (categoría news). Hereda timeRange=week del planner cuando detecta intent \"news\".", InputSchema: searchInputSchema()},
		{Name: "search_doc_oficial", Description: "Official documentation for the bounded SourceRudder registry. Use filters.library to select a registered library and filters.version as a requested, unverified version. Successful validated results use strategy=\"official_doc_registry_search\"; unknown, ambiguous, failed, or unvalidated searches use strategy=\"official_doc_web_fallback\".", InputSchema: officialDocsInputSchema()},
		{Name: "search_local_index", Description: "Searches an operator-curated read-only corpus with deterministic lexical ranking and strategy=\"local_index_lexical\". When LOCAL_INDEX_PATH is unset, returns strategy=\"local_index_unavailable\" without web fallback.", InputSchema: searchInputSchema()},
		{Name: "search_github", Description: "Búsqueda en GitHub: repositorios, archivos y commits. Backend: GitHub API.", InputSchema: searchInputSchema()},
		{Name: "search_github_pr", Description: "Pull requests en GitHub. Acepta filters.state=open|closed. Backend: GitHub API.", InputSchema: githubFiltersInputSchema()},
		{Name: "search_github_issue", Description: "Issues en GitHub. Acepta filters.state=open|closed. Backend: GitHub API.", InputSchema: githubFiltersInputSchema()},
		{Name: "search_stackoverflow", Description: "Q&A técnica en StackOverflow. Backend: StackOverflow API.", InputSchema: searchInputSchema()},
		{Name: "search_npm", Description: "Paquetes Node/TS en el registro npm.", InputSchema: searchInputSchema()},
		{Name: "search_nuget", Description: "Paquetes .NET en NuGet Gallery.", InputSchema: searchInputSchema()},
		{Name: "search_pypi", Description: "Paquetes Python en PyPI.", InputSchema: searchInputSchema()},
		{Name: "search_docker_hub", Description: "Imágenes Docker en Docker Hub.", InputSchema: searchInputSchema()},
		{Name: "search_academic", Description: "Papers, preprints y referencias académicas. Backend: SearxNG (arxiv).", InputSchema: searchInputSchema()},
		{Name: "search_reddit", Description: "Discusiones y experiencias reales en Reddit. Backend: SearXNG local, con posts públicos indexados y filtro reddit.com. Devuelve strategy=\"searxng_reddit_index\"; los fallos del proveedor se señalan como partial con warnings.", InputSchema: searchInputSchema()},
		{Name: "search_youtube", Description: "Tutoriales y demos en YouTube. Backend: SearxNG (youtube,brave).", InputSchema: searchInputSchema()},
		{Name: "search_images", Description: "Image search through SearXNG. Candidates are validated and ranked by bounded whole-word lexical hints in title and content before applying maxResults; this does not infer semantic relevance, and partial upstream warnings remain visible.", InputSchema: searchInputSchema()},
		{Name: "fetch_url", Description: "Obtener el HTML de una URL con extracción básica de title y metadata. Usa fetch_and_extract si necesitas el contenido principal. SSRF bloquea localhost/privados.", InputSchema: fetchURLInputSchema()},
		{Name: "fetch_and_extract", Description: "Extraer el contenido principal de una URL según el modo (auto/article/documentation/raw). Ignora timeoutMs.", InputSchema: fetchAndExtractInputSchema()},
		{Name: "extract_structured", Description: "Extraer tablas, metadata y estructura de una URL como JSON en Content. Útil para páginas con datos tabulares. Ignora mode y timeoutMs.", InputSchema: fetchURLInputSchema()},
		{Name: "validate_url", Description: "Verificar accesibilidad y seguridad (no SSRF) de una URL. Devuelve {url, valid, error}.", InputSchema: urlInputSchema()},
		{Name: "check_link_status", Description: "Validar hasta 100 URLs en paralelo (200 ms entre requests). Devuelve [{url, valid, status, error}] en el mismo orden que el input.", InputSchema: urlListInputSchema()},
		{Name: "summarize_results", Description: "Síntesis breve de un array de SearchResultItem. Devuelve {summary, keyFindings, citations, confidence}. No acepta style ni goal.", InputSchema: synthesisInputSchema()},
		{Name: "deep_research", Description: "Síntesis consolidada con agrupación por temas heurísticos. Devuelve {summary, themes[], keyFindings, comparison{}, confidence}. No acepta style ni goal.", InputSchema: synthesisInputSchema()},
		{Name: "compare_sources", Description: "Comparar SearchResultItem entre sí. Devuelve {sources[], consensus, divergences[]}. No acepta style ni goal.", InputSchema: synthesisInputSchema()},
		{Name: "get_cached", Description: "Recupera una entrada de la caché en proceso por clave exacta. Devuelve {cache_hit:bool, entry:CacheEntry|null}. Si no hay entrada o expiró, devuelve cache_hit=false sin error.", InputSchema: cachedEntryInputSchema()},
		{Name: "invalidate_cache", Description: "Elimina una entrada de la caché en proceso por clave exacta. Devuelve {key:string, invalidated:bool}. Idempotente: cuando la clave no existe, devuelve invalidated=false sin error.", InputSchema: cachedEntryInputSchema()},
		{Name: "get_search_history", Description: "Lista las últimas invocaciones de búsqueda retenidas en el proceso (newest-first). Argumentos: limit (int, requerido) y query opcional como filtro substring case-sensitive sobre la Query almacenada. Devuelve {history:Entry[], limit:int, query:string}.", InputSchema: historyInputSchema()},
		{Name: "get_current_date", Description: "Fecha y hora UTC consistente para citación: {date, time, timezone:\"UTC\", timestamp}.", InputSchema: emptyInputSchema()},
	}
}

func searchInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query":      map[string]interface{}{"type": "string", "description": "Texto de búsqueda (requerido)"},
			"maxResults": map[string]interface{}{"type": "integer", "description": "Máximo de resultados (default 10, lo aplica el planner)"},
			"language":   map[string]interface{}{"type": "string", "description": "Código BCP-47, ej: 'en', 'es'. Solo se reenvía a SearxNG."},
			"safeSearch": map[string]interface{}{"type": "boolean", "description": "Activar safesearch en SearxNG (default false)"},
			"timeRange": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"", "day", "week", "month", "year"},
				"description": "Rango temporal que se reenvía a SearxNG. Vacío = sin restricción.",
			},
		},
		"required": []string{"query"},
	}
}

// githubFiltersInputSchema is the variant of searchInputSchema that
// exposes the filters bag used by search_github_pr and
// search_github_issue. The connector reads filters.state to narrow by
// open / closed.
func githubFiltersInputSchema() map[string]interface{} {
	schema := searchInputSchema()
	props := schema["properties"].(map[string]interface{})
	props["filters"] = map[string]interface{}{
		"type":        "object",
		"description": "Filtros específicos del conector. Solo search_github_pr / search_github_issue leen filters.state ('open' | 'closed').",
		"properties": map[string]interface{}{
			"state": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"open", "closed"},
				"description": "Estado del PR o issue (solo search_github_pr / search_github_issue)",
			},
		},
	}
	return schema
}

func officialDocsInputSchema() map[string]interface{} {
	schema := searchInputSchema()
	props := schema["properties"].(map[string]interface{})
	props["filters"] = map[string]interface{}{
		"type":        "object",
		"description": "Official-documentation registry selectors.",
		"properties": map[string]interface{}{
			"library": map[string]interface{}{"type": "string", "description": "Registered library ID or alias. Takes precedence over query inference."},
			"version": map[string]interface{}{"type": "string", "description": "Requested version. It is not verified unless returned provenance explicitly identifies it."},
		},
	}
	return schema
}

func fetchURLInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "URL http(s) a obtener (requerido). SSRF bloquea localhost, *.local y privadas."},
		},
		"required": []string{"url"},
	}
}

func fetchAndExtractInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "URL http(s) a obtener (requerido). SSRF bloquea localhost, *.local y privadas."},
			"mode": map[string]interface{}{
				"type":        []string{"string", "null"},
				"enum":        []string{"", "auto", "article", "documentation", "raw"},
				"description": "Modo de extracción del contenido principal. auto=detección por defecto; article=texto de artículo; documentation=texto de página de docs; raw=HTML sin extracción.",
			},
		},
		"required": []string{"url"},
	}
}

func urlInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "URL a validar (requerido). SSRF bloquea localhost, *.local y privadas."},
		},
		"required": []string{"url"},
	}
}

func urlListInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"urls": map[string]interface{}{"type": "array", "maxItems": 100, "items": map[string]interface{}{"type": "string"}, "description": "Lista de hasta 100 URLs a validar en paralelo (requerido)."},
		},
		"required": []string{"urls"},
	}
}

// searchResultItemSchema is the JSON schema that the synthesis tools
// expect for each element of the `results` array. Synthesizers read
// title, url, snippet, source, score, publishedAt, author,
// citationId. Agents composing summarize_results / deep_research /
// compare_sources should use this shape as their input contract.
func searchResultItemSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title":        map[string]interface{}{"type": "string", "description": "Título del resultado"},
			"url":          map[string]interface{}{"type": "string", "description": "URL canónica del resultado"},
			"snippet":      map[string]interface{}{"type": "string", "description": "Resumen o extracto"},
			"source":       map[string]interface{}{"type": "string", "description": "Nombre del conector que produjo el item"},
			"type":         map[string]interface{}{"type": "string", "description": "Tipo de resultado (ej: 'web', 'article')"},
			"score":        map[string]interface{}{"type": "number", "description": "Puntuación de relevancia"},
			"publishedAt":  map[string]interface{}{"type": "string", "format": "date-time", "description": "Fecha de publicación en RFC3339"},
			"author":       map[string]interface{}{"type": "string", "description": "Autor"},
			"tags":         map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Etiquetas"},
			"citationId":   map[string]interface{}{"type": "string", "description": "Identificador estable entre invocaciones (lo produce el conector)"},
			"canonicalUrl": map[string]interface{}{"type": "string", "description": "URL canónica normalizada (la produce el connector manager)"},
		},
		"required": []string{"title", "url", "source"},
	}
}

func synthesisInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "Consulta original (requerido)"},
			"results": map[string]interface{}{
				"type":        "array",
				"description": "Resultados a sintetizar (requerido). Cada elemento sigue el schema SearchResultItem; usa el campo citationId si necesitas enlazar con un SearchResponse cacheado.",
				"items":       searchResultItemSchema(),
			},
		},
		"required": []string{"query"},
	}
}

func emptyInputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

// cachedEntryInputSchema is the schema shared by get_cached and
// invalidate_cache. Both take a single required `key` string so the
// MCP client never has to guess which argument to pass.
func cachedEntryInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Clave exacta de la caché en proceso (requerido). La búsqueda es case-sensitive y NO hash-ea el input.",
			},
		},
		"required": []string{"key"},
	}
}

// historyInputSchema is the schema for get_search_history. limit is
// required so the MCP client always picks a cap; query is optional
// and narrows the result by substring (case-sensitive) over the
// stored Query field.
func historyInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Máximo de entradas a devolver (requerido). El servicio de historial acota por su bound interno y por este limit; lo que sea menor gana.",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Filtro substring case-sensitive sobre la Query almacenada. Vacío = sin filtro.",
			},
		},
		"required": []string{"limit"},
	}
}

func (s *Server) Tools() []Tool {
	return s.toolsRegistry
}

// Resources returns the static MCP resources exposed by this Server.
// Tests and any future in-process consumer can iterate the slice
// without having to round-trip through HandleResourcesList.
func (s *Server) Resources() []Resource {
	return s.resourcesRegistry
}

func (s *Server) HandleInitialize(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var req struct {
		ClientID           string                 `json:"clientId"`
		ProtocolVersion    string                 `json:"protocolVersion"`
		ClientCapabilities map[string]interface{} `json:"clientCapabilities"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if req.ClientID == "" {
		req.ClientID = "anonymous-" + uuid.New().String()[:8]
	}
	sessionID := uuid.New().String()
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]interface{}{
			"name":    serverName,
			"version": serverVersion,
		},
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{"listChanged": false},
			"resources": map[string]interface{}{"listChanged": false, "subscribe": false},
		},
		"sessionId": sessionID,
	}, nil
}

func (s *Server) HandleToolsList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var req struct {
		Filter struct {
			Capability string `json:"capability"`
			Tag        string `json:"tag"`
		} `json:"filter"`
	}
	_ = json.Unmarshal(params, &req)
	tools := make([]map[string]interface{}, 0, len(s.toolsRegistry))
	for _, t := range s.toolsRegistry {
		tools = append(tools, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return map[string]interface{}{"tools": tools}, nil
}

func (s *Server) HandleToolsCall(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var req struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	for _, t := range s.toolsRegistry {
		if t.Name == req.Name {
			inputJSON, _ := json.Marshal(req.Arguments)
			result, err := s.invokeTool(ctx, t, inputJSON)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": formatResult(result)},
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("tool not found: %s", req.Name)
}

func (s *Server) invokeTool(ctx context.Context, tool Tool, args json.RawMessage) (interface{}, error) {
	started := time.Now()
	result, err := tool.Handler(ctx, args)
	if strings.HasPrefix(tool.Name, "search_") {
		s.met.RecordSearchLatency(strings.TrimPrefix(tool.Name, "search_"), time.Since(started).Seconds())
	}
	if err == nil {
		s.met.IncrToolCall(tool.Name)
	}
	return result, err
}

func formatResult(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func (s *Server) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	switch r.Method {
	case http.MethodGet:
		s.handleHTTPGet(w, r)
	case http.MethodPost:
		s.handleHTTPPost(w, r)
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHTTPGet(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	writePing := func() error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		_, err := fmt.Fprint(w, ": ping\n\n")
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.WriteHeader(http.StatusOK)
	if err := writePing(); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := writePing(); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (s *Server) handleHTTPPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRPCRequestBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]interface{}{
			"jsonrpc": "2.0",
			"error":   map[string]interface{}{"code": -32700, "message": "parse error"},
		})
		return
	}
	resp, isNotification := s.handleRPCRequest(r.Context(), req)
	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRPCRequest(ctx context.Context, req rpcRequest) (interface{}, bool) {
	isNotification := req.ID == nil
	var resp interface{}
	switch req.Method {
	case "initialize":
		resp = s.handleMCPInitialize(req.ID)
	case "tools/list", "mcp.tools.list":
		resp = s.handleMCPToolsList(req.ID)
	case "tools/call", "mcp.tools.call":
		resp = s.handleMCPToolsCall(ctx, req.ID, req.Params)
	case "resources/list", "mcp.resources.list":
		resp = s.handleMCPResourcesList(req.ID, req.Params)
	case "resources/read", "mcp.resources.read":
		resp = s.handleMCPResourcesRead(req.ID, req.Params)
	case "ping":
		resp = map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": map[string]interface{}{}}
	default:
		if !isNotification {
			resp = map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]interface{}{"code": -32601, "message": fmt.Sprintf("method not found: %s", req.Method)},
			}
		}
	}
	return resp, isNotification
}

func (s *Server) handleMCPInitialize(id interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]interface{}{"name": serverName, "version": serverVersion},
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{"listChanged": false},
				"resources": map[string]interface{}{"listChanged": false, "subscribe": false},
			},
		},
	}
}

func (s *Server) handleMCPResourcesList(id interface{}, params json.RawMessage) map[string]interface{} {
	res, err := s.HandleResourcesList(context.Background(), params)
	if err != nil {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]interface{}{"code": -32603, "message": err.Error()},
		}
	}
	return map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": res}
}

func (s *Server) handleMCPResourcesRead(id interface{}, params json.RawMessage) map[string]interface{} {
	res, err := s.HandleResourcesRead(context.Background(), params)
	if err != nil {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]interface{}{"code": -32602, "message": err.Error()},
		}
	}
	return map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": res}
}

func (s *Server) handleMCPToolsList(id interface{}) map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(s.toolsRegistry))
	for _, t := range s.toolsRegistry {
		tools = append(tools, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  map[string]interface{}{"tools": tools},
	}
}

func (s *Server) handleMCPToolsCall(ctx context.Context, id interface{}, params json.RawMessage) map[string]interface{} {
	var reqParams struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &reqParams); err != nil {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]interface{}{"code": -32602, "message": "invalid params: " + err.Error()},
		}
	}
	if reqParams.Name == "" {
		return map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]interface{}{"code": -32602, "message": "name is required"},
		}
	}
	for _, t := range s.toolsRegistry {
		if t.Name == reqParams.Name {
			inputJSON, _ := json.Marshal(reqParams.Arguments)
			result, err := s.invokeTool(ctx, t, inputJSON)
			if err != nil {
				return map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"error":   map[string]interface{}{"code": -32603, "message": err.Error()},
				}
			}
			return map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": formatResult(result)},
					},
				},
			}
		}
	}
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]interface{}{"code": -32602, "message": fmt.Sprintf("tool not found: %s", reqParams.Name)},
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode HTTP response: %v", err)
	}
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key, Mcp-Session-Id, Accept")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

func (s *Server) Start(ctx context.Context) error {
	if s.transport != "http" {
		return nil
	}
	httpSrv := &http.Server{
		Addr:              s.httpAddr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
	s.httpSrv = httpSrv
	// Listen synchronously so address-conflict errors surface to the
	// caller instead of being silently logged from a goroutine.
	ln, err := net.Listen("tcp", s.httpAddr)
	if err != nil {
		s.httpSrv = nil
		return fmt.Errorf("listen %s: %w", s.httpAddr, err)
	}
	s.httpLn = ln
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server %s: %v", s.httpAddr, err)
		}
	}()
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	err := s.httpSrv.Shutdown(ctx)
	s.httpSrv = nil
	if s.httpLn != nil {
		_ = s.httpLn.Close()
		s.httpLn = nil
	}
	return err
}

func (s *Server) Name() string {
	return s.transport
}
