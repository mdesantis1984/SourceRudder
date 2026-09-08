# Conectar un cliente MCP

IA_Buscar admite dos formas de conexión: HTTP para el stack de Docker Compose y
`stdio` para ejecutar el binario como proceso local. Elija una sola opción por
cliente.

## Docker Compose por HTTP

Inicie el stack desde la raíz del repositorio siguiendo el
[inicio rápido](../README.md#docker-compose-recomendado). El endpoint MCP queda
disponible en `http://127.0.0.1:8080/mcp` y exige el token
`IA_BUSCAR_AUTH_KEY` guardado en `.env`.

Antes de abrir un cliente gráfico, exponga el mismo token en el entorno desde el
que se inicia el cliente:

```bash
export IA_BUSCAR_AUTH_KEY='<value-from-.env>'
```

### Cursor

Guarde esta configuración en `.cursor/mcp.json` para el proyecto o en
`~/.cursor/mcp.json` para todos los proyectos:

```json
{
  "mcpServers": {
    "ia-buscar": {
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer ${env:IA_BUSCAR_AUTH_KEY}"
      }
    }
  }
}
```

### OpenCode

Agregue el servidor a `opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ia-buscar": {
      "type": "remote",
      "url": "http://127.0.0.1:8080/mcp",
      "oauth": false,
      "headers": {
        "Authorization": "Bearer {env:IA_BUSCAR_AUTH_KEY}"
      },
      "enabled": true
    }
  }
}
```

Compruebe la conexión con `opencode mcp list`.

## Binario local por stdio

Compile el binario y mantenga SearXNG disponible. Si ya inició Docker Compose,
SearXNG está expuesto sólo en loopback mediante `http://127.0.0.1:8888`.

```bash
go build -o bin/ia-buscar ./cmd/ia-buscar
```

Use una ruta absoluta al binario. La autenticación Bearer no se aplica a
`stdio` porque el cliente crea y controla directamente el proceso.

### Claude Desktop y Cursor

Claude Desktop utiliza `claude_desktop_config.json`; Cursor utiliza
`.cursor/mcp.json` o `~/.cursor/mcp.json`. En ambos casos, la entrada del
servidor tiene esta forma:

```json
{
  "mcpServers": {
    "ia-buscar": {
      "command": "/ABSOLUTE/PATH/IA_Buscar/bin/ia-buscar",
      "args": [
        "-transport",
        "stdio",
        "-searxng-url",
        "http://127.0.0.1:8888"
      ]
    }
  }
}
```

### Visual Studio Code

Guarde esta configuración en `.vscode/mcp.json`:

```json
{
  "servers": {
    "iaBuscar": {
      "type": "stdio",
      "command": "/ABSOLUTE/PATH/IA_Buscar/bin/ia-buscar",
      "args": [
        "-transport",
        "stdio",
        "-searxng-url",
        "http://127.0.0.1:8888"
      ]
    }
  }
}
```

### OpenCode

Agregue el proceso local a `opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ia-buscar": {
      "type": "local",
      "command": [
        "/ABSOLUTE/PATH/IA_Buscar/bin/ia-buscar",
        "-transport",
        "stdio",
        "-searxng-url",
        "http://127.0.0.1:8888"
      ],
      "enabled": true
    }
  }
}
```

## Verificación

Reinicie el cliente después de modificar su configuración. Una conexión válida
debe mostrar 28 herramientas y el recurso
`agent-guide://ia-buscar/wire-contract`.

Para verificar el endpoint HTTP sin un cliente:

```bash
set -a
. ./.env
set +a
curl -fsS \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${IA_BUSCAR_AUTH_KEY}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' \
  http://127.0.0.1:8080/mcp
```

Un `401` indica que el token del cliente no coincide con `.env`. Si el cliente
no descubre herramientas, confirme que la URL termina en `/mcp` y revise sus
logs MCP. Una búsqueda puede devolver resultados vacíos o `partial:true` cuando
los proveedores públicos de SearXNG están degradados; eso no implica que la
conexión MCP haya fallado.

## Referencias de clientes

- [Cursor MCP](https://cursor.com/docs/mcp)
- [OpenCode MCP servers](https://opencode.ai/docs/mcp-servers/)
- [Visual Studio Code MCP configuration](https://code.visualstudio.com/docs/agents/reference/mcp-configuration)
- [Claude Desktop local MCP servers](https://support.claude.com/en/articles/10949351-getting-started-with-local-mcp-servers-on-claude-desktop)
