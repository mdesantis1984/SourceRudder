# Clientes MCP

[English](mcp-clients.md) | [Español](mcp-clients.es.md)

Conecte cada cliente a SourceRudder por HTTP o `stdio`, no ambas opciones para la misma entrada. Una conexión correcta expone 28 herramientas y el recurso `agent-guide://sourcerudder/wire-contract`.

## HTTP con Compose

Inicie el stack y deje `SOURCERUDDER_AUTH_KEY` disponible para el proceso del cliente:

```bash
export SOURCERUDDER_AUTH_KEY='<valor-de-.env>'
```

La URL MCP predeterminada es `http://127.0.0.1:8080/mcp`.

### Cursor

Guarde en `.cursor/mcp.json` o `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "sourcerudder": {
      "url": "http://127.0.0.1:8080/mcp",
      "headers": { "Authorization": "Bearer ${env:SOURCERUDDER_AUTH_KEY}" }
    }
  }
}
```

### OpenCode

Agregue a `opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sourcerudder": {
      "type": "remote",
      "url": "http://127.0.0.1:8080/mcp",
      "oauth": false,
      "headers": { "Authorization": "Bearer {env:SOURCERUDDER_AUTH_KEY}" },
      "enabled": true
    }
  }
}
```

Verifique con `opencode mcp list`.

## stdio local

Use una ruta absoluta a `sourcerudder` y confirme que SearXNG sea accesible, habitualmente en `http://127.0.0.1:8888`. El Bearer HTTP no aplica porque el cliente administra el proceso.

### Claude Desktop y Cursor

En `claude_desktop_config.json` o `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "sourcerudder": {
      "command": "/RUTA/ABSOLUTA/sourcerudder",
      "args": ["-transport", "stdio", "-searxng-url", "http://127.0.0.1:8888"]
    }
  }
}
```

### Visual Studio Code

Guarde en `.vscode/mcp.json`:

```json
{
  "servers": {
    "sourcerudder": {
      "type": "stdio",
      "command": "/RUTA/ABSOLUTA/sourcerudder",
      "args": ["-transport", "stdio", "-searxng-url", "http://127.0.0.1:8888"]
    }
  }
}
```

### OpenCode

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sourcerudder": {
      "type": "local",
      "command": ["/RUTA/ABSOLUTA/sourcerudder", "-transport", "stdio", "-searxng-url", "http://127.0.0.1:8888"],
      "enabled": true
    }
  }
}
```

## Verificar el contrato wire

Reinicie el cliente luego de cambiar configuración. Para HTTP use este request JSON-RPC:

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${SOURCERUDDER_AUTH_KEY}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  http://127.0.0.1:8080/mcp
```

Las 28 herramientas son `search_web`, `search_news`, `search_doc_oficial`, `search_local_index`, `search_github`, `search_github_pr`, `search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`, `search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`, `search_youtube`, `search_images`, `fetch_url`, `fetch_and_extract`, `extract_structured`, `validate_url`, `check_link_status`, `summarize_results`, `deep_research`, `compare_sources`, `get_cached`, `invalidate_cache`, `get_search_history` y `get_current_date`.

Conserve el formato JSON devuelto, los nombres de conectores y los strategy IDs. En particular, `search_doc_oficial` devuelve `official_doc_registry_search` u `official_doc_web_fallback`; el índice local opcional devuelve `local_index_lexical` o `local_index_unavailable`; y Reddit usa `searxng_reddit_index`. Un `401` indica que la credencial HTTP no coincide. Resultados vacíos o `partial:true` pueden señalar degradación de proveedor, no una falla de conexión MCP.

## Referencias de clientes

- [Cursor MCP](https://cursor.com/docs/mcp)
- [OpenCode MCP servers](https://opencode.ai/docs/mcp-servers/)
- [Visual Studio Code MCP configuration](https://code.visualstudio.com/docs/agents/reference/mcp-configuration)
- [Claude Desktop local MCP servers](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)
