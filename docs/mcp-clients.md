# MCP clients

[English](mcp-clients.md) | [Español](mcp-clients.es.md)

Connect each client to SourceRudder through either HTTP or `stdio`, not both for the same server entry. A successful connection exposes 28 tools and the guide resource `agent-guide://sourcerudder/wire-contract`.

## HTTP with Compose

Start the stack, then make `SOURCERUDDER_AUTH_KEY` available to the desktop client process:

```bash
export SOURCERUDDER_AUTH_KEY='<value-from-.env>'
```

The default MCP URL is `http://127.0.0.1:8080/mcp`.

### Cursor

Save in `.cursor/mcp.json` or `~/.cursor/mcp.json`:

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

Add to `opencode.json`:

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

Verify with `opencode mcp list`.

## Local stdio

Use an absolute path to `sourcerudder` and ensure SearXNG is reachable, commonly at `http://127.0.0.1:8888`. HTTP Bearer authentication does not apply because the client owns the process.

### Claude Desktop and Cursor

In `claude_desktop_config.json` or `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "sourcerudder": {
      "command": "/ABSOLUTE/PATH/sourcerudder",
      "args": ["-transport", "stdio", "-searxng-url", "http://127.0.0.1:8888"]
    }
  }
}
```

### Visual Studio Code

Save in `.vscode/mcp.json`:

```json
{
  "servers": {
    "sourcerudder": {
      "type": "stdio",
      "command": "/ABSOLUTE/PATH/sourcerudder",
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
      "command": ["/ABSOLUTE/PATH/sourcerudder", "-transport", "stdio", "-searxng-url", "http://127.0.0.1:8888"],
      "enabled": true
    }
  }
}
```

## Verify the wire contract

Restart the client after changing configuration. For HTTP, use the following JSON-RPC request:

```bash
curl -fsS \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${SOURCERUDDER_AUTH_KEY}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  http://127.0.0.1:8080/mcp
```

The 28 tool names are `search_web`, `search_news`, `search_doc_oficial`, `search_local_index`, `search_github`, `search_github_pr`, `search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`, `search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`, `search_youtube`, `search_images`, `fetch_url`, `fetch_and_extract`, `extract_structured`, `validate_url`, `check_link_status`, `summarize_results`, `deep_research`, `compare_sources`, `get_cached`, `invalidate_cache`, `get_search_history`, and `get_current_date`.

Keep the returned JSON shape, connector names, and strategy IDs intact. In particular, `search_doc_oficial` returns `official_doc_registry_search` or `official_doc_web_fallback`; the optional local index returns `local_index_lexical` or `local_index_unavailable`; and Reddit uses `searxng_reddit_index`. A `401` means the HTTP credential does not match. Empty or `partial:true` search results can indicate provider degradation rather than an MCP connection failure.

## Client references

- [Cursor MCP](https://cursor.com/docs/mcp)
- [OpenCode MCP servers](https://opencode.ai/docs/mcp-servers/)
- [Visual Studio Code MCP configuration](https://code.visualstudio.com/docs/agents/reference/mcp-configuration)
- [Claude Desktop local MCP servers](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)
