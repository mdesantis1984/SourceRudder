package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Resource models an MCP resource exposed through resources/list and
// resources/read. The MCP spec (2024-11-05) requires uri, name,
// mimeType, and a textual or binary blob; we only ship text resources
// for now, so the body lives in Text.
type Resource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Text        string
}

// AgentGuideURI is the single static resource we expose today. It is
// stable: clients may cache it across requests. Do not rename the URI
// without an explicit version bump and changelog entry, since AI
// agents hardcode it into their tool catalogues.
const AgentGuideURI = "agent-guide://ia-buscar/wire-contract"

// agentGuideMIMEType is text/markdown so MCP clients can render it as
// documentation rather than trying to execute it.
const agentGuideMIMEType = "text/markdown"

// wireContractGuide is the static Spanish guide that teaches AI agents
// how to call IA_Buscar's tools and how to read the responses. The
// content is intentionally hand-curated: every tool and field mentioned
// here must exist in this Server's registry today, or the behavioural
// tests in resources_test.go will fail.
//
// Source code, comments, and identifiers stay English; this single
// string is the only place where the guide is allowed to be Spanish,
// because the audience is the agent operator, not the codebase
// maintainer.
const wireContractGuide = `# Guía para agentes IA — Wire Contract de IA_Buscar

Esta guía describe cómo invocar correctamente las 25 tools MCP registradas por IA_Buscar
y cómo interpretar cada respuesta. Léela una vez antes de construir tu primer plan de búsqueda.

## 1. Cinco familias de tools

| Familia       | Tools                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | Cuándo usarla |
|---------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------|
| Búsqueda      | search_web, search_news, search_doc_oficial, search_local_index, search_github, search_github_pr, search_github_issue, search_stackoverflow, search_npm, search_nuget, search_pypi, search_docker_hub, search_academic, search_reddit, search_youtube, search_images                                                                                                                                                                                                                                                                                                                                  | Obtener una lista de SearchResultItem para responder a una pregunta. |
| Fetch/extract | fetch_url, fetch_and_extract, extract_structured                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | Traer el contenido de una URL específica que ya conoces. |
| Validación    | validate_url, check_link_status                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Comprobar accesibilidad y seguridad (no SSRF) de una URL o lote de URLs. |
| Síntesis      | summarize_results, deep_research, compare_sources                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Resumir, organizar o comparar un array de SearchResultItem. |
| Tiempo        | get_current_date                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Obtener fecha/hora UTC consistente para citación. |

## 2. Familia de búsqueda — cuándo invocar cada tool

- search_web — fallback general. Usa SearxNG y respeta language, timeRange, safeSearch.
- search_news — artículos recientes. Resuelve con SearxNG (categoría news). El planner resuelve timeRange="week" cuando detecta intent "news".
- search_doc_oficial — NO consulta un índice curado de documentación. Hoy hace fallback a search_web y devuelve strategy="official_doc_web_fallback" con un warning. Si ves ese strategy, sabes que los resultados vinieron de búsqueda web general, no de un índice curado de documentación.
- search_local_index — NO redirige a búsqueda web. Mientras no haya un proveedor real de índice local configurado, devuelve strategy="local_index_unavailable", results=[] y un warning local_index_unavailable. No lo confundas con un resultado vacío real: es una señal de "esta tool no está wired todavía".
- search_github, search_github_pr, search_github_issue — endpoints de GitHub. search_github_pr y search_github_issue leen filters.state ("open" / "closed") para reducir el resultado.
- search_stackoverflow, search_npm, search_nuget, search_pypi, search_docker_hub, search_academic, search_youtube, search_images — conectores dedicados a un proveedor.
- search_reddit — posts públicos de Reddit indexados por el SearXNG local. Agrega la restricción site:reddit.com, acepta solo URLs de hilos públicos y devuelve strategy="searxng_reddit_index". Un fallo de SearXNG devuelve partial=true con warnings; un resultado 200 vacío es un resultado vacío real.

## 3. Input schema estable para tools de búsqueda

` + "```json" + `
{
  "query": "<string, requerido>",
  "maxResults": <integer, default 10>,
  "language": "<string BCP-47, ej: 'en', 'es'>",
  "safeSearch": <boolean, default false>,
  "timeRange": "<enum: '' | 'day' | 'week' | 'month' | 'year'>"
}
` + "```" + `

timeRange se reenvía a SearxNG; los conectores que no hablan SearxNG lo ignoran en silencio. No envíes valores fuera de ese enum; el servidor los aceptará pero el upstream los rechazará.

## 4. SearchResponse — campos estables

| Campo      | Tipo               | Significado |
|------------|--------------------|-------------|
| query      | string             | El query que recibió el server. |
| results    | SearchResultItem[] | SIEMPRE un array (nunca null, nunca ausente), aunque esté vacío. Cada elemento es SearchResultItem. |
| strategy   | string, opcional   | Backend concreto que respondió: "searxng", "searxng_reddit_index", "official_doc_web_fallback", "local_index_unavailable". Te dice si la tool habló con un proveedor real. |
| cached     | bool, opcional     | true si la respuesta vino del caché en proceso. |
| partial    | bool, opcional     | true cuando hubo degradación upstream (timeouts, 5xx, 429). Distingue "el upstream nos dio una respuesta incompleta" de "todo OK". |
| warnings   | string[], opcional | Lista de advertencias accionables (configuración, intent del planner, etc.). |
| errors     | string[], opcional | Lista de errores que impidieron recuperar datos. Se omite cuando está vacía. |

summary, keyFindings, sourcesUsed, confidence siguen presentes para compatibilidad pero NO son la fuente de verdad. La síntesis real se hace con las tools dedicadas de la familia 5.

### Cómo distinguir resultado vacío saludable vs degradado vs unconfigured

| Caso                                 | results | strategy                              | partial | errors | Notas |
|--------------------------------------|---------|---------------------------------------|----------|--------|-------|
| healthy empty (resultado vacío sano) | []      | "searxng", "searxng_reddit_index", "npm", etc.      | false    | []     | El upstream respondió 200 con cero items. No es degradación. |
| Upstream degradado (timeouts, 5xx)   | []      | nombre del conector                   | true     | [err]  | Hubo un error de transporte o HTTP >= 400 no recuperable. |
| Unconfigured (local_index)           | []      | "local_index_unavailable" | false    | [tag]  | La tool no habló con ningún proveedor real. |
| Fallback explícito (doc_oficial)     | [items] | "official_doc_web_fallback"           | false    | []     | Los resultados vienen del fallback web, no de docs oficiales. |

Regla práctica: si strategy empieza por "unavailable" o "unconfigured", la tool no habló con ningún proveedor real — no generes contenido a partir de sus resultados.

### SearchResultItem

` + "```json" + `
{
  "title": "<string>",
  "url": "<string>",
  "snippet": "<string>",
  "source": "<string, nombre del conector>",
  "type": "<string, ej: 'web', 'article'>",
  "score": <number, opcional>,
  "publishedAt": "<RFC3339 timestamp, opcional>",
  "author": "<string, opcional>",
  "tags": ["<string>"],
  "citationId": "<string, opcional>",
  "canonicalUrl": "<string, opcional>"
}
` + "```" + `

citationId se puede usar como clave estable entre invocaciones para resumir resultados o enlazar con un SearchResponse cacheado.

## 5. Familia de fetch/extract

fetch_url(url) — devuelve un FetchResponse (campos url, title, content, metadata, warnings) con extracción básica de title y metadata. Útil cuando quieres leer un documento en crudo.

fetch_and_extract(url, mode) — devuelve un FetchResponse con el contenido principal extraído según el modo. Modos aceptados: "auto" (default, intenta detectar el contenido principal), "article" (texto de un artículo), "documentation" (texto de una página de docs), "raw" (HTML sin extracción).

extract_structured(url) — devuelve un FetchResponse con tablas y metadata como un mapa JSON serializado en Content; ideal para páginas con datos tabulares.

Input schema estable:

` + "```json" + `
{
  "url": "<string, requerido, http(s) only>"
}
` + "```" + `

Para fetch_and_extract se agrega:

` + "```json" + `
"mode": "<enum: 'auto' | 'article' | 'documentation' | 'raw'>"
` + "```" + `

Notas: fetch_url y extract_structured IGNORAN mode. Los tres tools IGNORAN timeoutMs: el timeout de cada request es una decisión del server (boot-time --fetch-timeout-ms), no del cliente. La protección SSRF está activa — no se aceptan hosts "localhost", "*.local", IPs privadas (10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, 0/8).

## 6. Familia de validación

validate_url(url) → ` + "`" + `{url, valid, error}` + "`" + `. valid=true si el HEAD devolvió < 400 y la URL pasó la protección SSRF. error contiene el motivo si valid=false.

check_link_status(urls) → ` + "`" + `[{url, valid, status, error}, ...]` + "`" + `. Rate limit interno: 200 ms entre requests. El orden de salida coincide con el orden de entrada.

## 7. Familia de síntesis

Las tres comparten el input schema:

` + "```json" + `
{
  "query": "<string, requerido>",
  "results": [SearchResultItem]
}
` + "```" + `

Nota: NO se aceptan ni se leen "style" ni "goal" en la versión actual — no los envíes, el server los descartará silenciosamente.

summarize_results(query, results) → ` + "`" + `{summary, keyFindings, citations, confidence}` + "`" + `. Cuando results=[], devuelve summary="No results to synthesize.", keyFindings=[], confidence=0.

deep_research(query, results) → ` + "`" + `{summary, themes[], keyFindings, comparison{}, confidence}` + "`" + `. Cada theme agrupa items por categoría heurística (Technical Documentation, Code & Programming, News & Updates, Packages & Dependencies, Community & Discussion, General).

compare_sources(query, results) → ` + "`" + `{sources[], consensus, divergences[]}` + "`" + `. sources[] incluye url, title, source, score, publishedAt, isMostRecent, snippetPreview.

## 8. Familia de tiempo

get_current_date() → ` + "`" + `{date, time, timezone, timestamp}` + "`" + `. date es YYYY-MM-DD, time es HH:MM:SS, timezone es siempre "UTC", timestamp es unix seconds. Úsala cuando cites "hoy" o necesites una fecha consistente entre tools.

## 9. Recursos para discoverability

agent-guide://ia-buscar/wire-contract (este documento) es accesible vía resources/read para que tu agente pueda releer la guía sin tener que memorizarla. Si tu MCP client soporta resources/list, lo verás anunciado automáticamente en initialize.

## 10. Errores que debes esperar

- "tool not found: <name>" — invocaste una tool que no existe. El server solo expone 25.
- "name is required" — falta el campo name en tools/call.
- "invalid args: <reason>" — el JSON de arguments no parsea contra el input schema. Revisa enums (timeRange, mode) y obligatorios (query, url, urls).

## 10b. Contrato de fetch (FetchResponse)

Los tools fetch, fetch_and_extract, extract_structured, validate_url y check_link_status comparten el mismo motor de fetch y exponen la misma FetchResponse con cuatro campos nuevos (a partir de 1.2.0):

- outcome — taxonomía fija de strings:
  - success — 2xx recibido.
  - blocked-target — DNS o texto detectó IP no pública / loopback / host interno. No se marca el dial.
  - blocked-redirect — un Location apuntaba a una IP no pública y fue rechazado.
  - too-many-redirects — la cadena excedió el cap (5 hops por defecto).
  - timeout — request lifecycle excedió el timeout configurado y no se reintentó.
  - transport-error — fallo TCP/TLS/DNS no clasificado como timeout.
  - http-error — status upstream no-2xx no retryable (404, 500 fuera de 502-504, etc.).
  - non-transient-failure — body/classification/argument errors.
  - transient-failure-retried-exhausted — se agotaron los reintentos sobre 429/502/503/504/timeout.
- status — código HTTP final, o 0 si nunca se recibió respuesta.
- redirectChain — lista de URLs seguidas, sin incluir el destino final.
- attempts — cantidad total de intentos (incluye reintentos). 1 = sin retry.

Configuración expuesta al operador:

- --fetch-timeout-ms (default 30000) — timeout del ciclo completo. También leíble vía env var FETCH_TIMEOUT_MS; el flag CLI gana cuando ambos están configurados.
- Variable de entorno FETCH_USER_AGENT (default: Mozilla compatible con IA-Buscar/1.2).
- FETCH_MAX_REDIRECTS (default 5).
- FETCH_MAX_ATTEMPTS (default 3).
- Backoff: exponencial con jitter determinístico, base 200ms.

Política de retry: SOLO 429 y 502-504 más timeouts del transporte se reintentan. 4xx fuera de 429 NO se reintenta y se clasifica como http-error.

SSRF: el motor resuelve A/AAAA en cada hop (target inicial Y cada redirect), rechaza el hop si ALGUNA dirección cae en rango no público (loopback, RFC1918, link-local, CGNAT, multicast, reservados), y diala solo a la IP aprobada preservando el Host header y TLS ServerName. Rebinding entre validación y dial queda bloqueado porque la IP se fija en Transport.DialContext.

search_reddit no llama a la API de Reddit: consulta el SearXNG configurado para descubrir posts públicos ya indexados. La estrategia searxng_reddit_index identifica este backend.

## 11. Versionado

El contrato SearchResponse y los nombres de tools están congelados en esta rama. Cambios incompatibles requieren bump mayor del servidor y un changelog explícito en el README. Los IDs de estrategia ("searxng_reddit_index", "local_index_unavailable", "official_doc_web_fallback") también son estables — puedes hacer pattern matching sobre ellos.

## 12. Changelog

- **1.3.0 release candidate** — search_reddit discovers public Reddit posts indexed by the configured SearXNG service and returns the stable ` + "`" + `searxng_reddit_index` + "`" + ` strategy. The SearchResponse schema is unchanged; deprecated Reddit user-agent and base-URL CLI options are no-ops retained for compatibility. This candidate has local Docker QA evidence only and is not deployed.
- **1.2.0** — Anonymous-only Reddit (sin OAuth, sin client_id/secret), release gate ejecutable con carve-out por nombre exacto de rama, fetch engine con outcomes explícitos (success / blocked-target / blocked-redirect / too-many-redirects / timeout / transport-error / http-error / non-transient-failure / transient-failure-retried-exhausted), degradación centralizada vía recordDegraded, cache key incluye TimeRange.
`

// buildResourcesRegistry returns the static MCP resources that
// resources/list advertises. Today there is exactly one: the agent
// guide. The list is intentionally small: agents should be able to
// read it without scanning noise.
func (s *Server) buildResourcesRegistry() {
	s.resourcesRegistry = []Resource{
		{
			URI:         AgentGuideURI,
			Name:        "ia-buscar-wire-contract",
			Description: "Guía en español para agentes IA: cuándo usar cada tool, cómo leer SearchResponse, cómo distinguir healthy empty de degradado y unconfigured, shapes de fetch/validación/síntesis/tiempo.",
			MimeType:    agentGuideMIMEType,
			Text:        wireContractGuide,
		},
	}
}

// HandleResourcesList is the JSON-RPC handler for resources/list.
// It is exposed for both the in-process and HTTP transports; tests
// exercise the HTTP path to prove end-to-end discoverability.
func (s *Server) HandleResourcesList(_ context.Context, _ json.RawMessage) (interface{}, error) {
	out := make([]map[string]interface{}, 0, len(s.resourcesRegistry))
	for _, r := range s.resourcesRegistry {
		out = append(out, map[string]interface{}{
			"uri":         r.URI,
			"name":        r.Name,
			"description": r.Description,
			"mimeType":    r.MimeType,
		})
	}
	return map[string]interface{}{"resources": out}, nil
}

// HandleResourcesRead is the JSON-RPC handler for resources/read.
// It looks up the resource by URI and returns its textual contents in
// the MCP standard envelope ({contents: [{uri, mimeType, text}]}).
// Unknown URIs return a JSON-RPC error with code -32002 (MCP reserves
// -32000 to -32099 for implementation-defined server errors); we use
// -32002 to match the convention other MCP servers adopt for
// "resource not found".
func (s *Server) HandleResourcesRead(_ context.Context, params json.RawMessage) (interface{}, error) {
	var req struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if strings.TrimSpace(req.URI) == "" {
		return nil, fmt.Errorf("uri is required")
	}
	for _, r := range s.resourcesRegistry {
		if r.URI == req.URI {
			return map[string]interface{}{
				"contents": []map[string]interface{}{
					{
						"uri":      r.URI,
						"name":     r.Name,
						"mimeType": r.MimeType,
						"text":     r.Text,
					},
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("resource not found: %s", req.URI)
}
