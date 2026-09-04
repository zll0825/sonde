# Research Assistant MCP Runbook

> How to configure and attach AI research assistants (Cursor, Claude Desktop, Claude Code) to Capital Observatory via the read-only Model Context Protocol (MCP) server.

---

## 1. Overview

`bin/mcp` (built from `cmd/mcp`) implements the standard [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) over `stdio` (and optionally HTTP/SSE). It provides a read-only query interface for AI assistants to inspect real-time anomaly alerts, frozen research snapshots, and ontology relationship graphs without running SQL or mutating pipeline state.

### Tools Exposed (Strictly Read-Only)

| Tool Name | Parameters | Description |
|-----------|------------|-------------|
| `list_active_alerts` | `limit` (int, opt, default 50) | Returns currently active anomaly alerts, ordered by trigger time descending. |
| `get_research_snapshot` | `alert_id` (string, req) | Returns the complete frozen research snapshot for an alert (trends, timeline, overlays, historical analogs). |
| `list_ontology_relations` | `entity_id` (string, opt) | Returns active ontology entity relationships. |
| `list_ontology_candidates` | `status` (string, opt, default "pending") | Returns discovered relationship candidates awaiting review. |

### Security Invariants

- **Zero Mutations**: No write tools exist. Commands like `backfill`, `sync`, feedback submission, or relation mutation are strictly rejected.
- **Local Isolation**: By default, `bin/mcp` communicates entirely through `stdin`/`stdout` within the local machine without opening any network ports.
- **No Store Leakage**: Queries read directly from PostgreSQL via `DATABASE_URL` without calling Core gRPC.

---

## 2. Quick Build

```bash
# Build binary to bin/mcp
make build-mcp
```

Verify the binary is operational:

```bash
./bin/mcp --help
```

---

## 3. Client Configurations

### A. Claude Desktop

Edit your Claude Desktop configuration file:
- **macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add `capital-observatory` under `mcpServers`:

```json
{
  "mcpServers": {
    "capital-observatory": {
      "command": "/ABSOLUTE/PATH/TO/capital_observatory/bin/mcp",
      "env": {
        "DATABASE_URL": "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
      }
    }
  }
}
```

Restart Claude Desktop. The hammer icon will show the 4 read-only tools available for prompts.

---

### B. Cursor

In Cursor:
1. Open **Settings** -> **Features** -> **MCP**.
2. Click **Add New MCP Server**.
3. Fill in:
   - **Name**: `capital-observatory`
   - **Type**: `command`
   - **Command**: `/ABSOLUTE/PATH/TO/capital_observatory/bin/mcp`
   - **Environment Variables**: `DATABASE_URL=postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable`

Alternatively, add it to your project-level `.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "capital-observatory": {
      "command": "./bin/mcp",
      "env": {
        "DATABASE_URL": "postgres://capital:capital_dev@localhost:5432/capital_observatory?sslmode=disable"
      }
    }
  }
}
```

---

## 4. Manual Stdio Testing

You can pipe JSON-RPC requests directly into `bin/mcp` from your terminal:

### Handshake (`initialize`)

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | ./bin/mcp
```

Response:
```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"capital-observatory-mcp","version":"0.1.0"}}}
```

### List Tools (`tools/list`)

```bash
echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | ./bin/mcp
```

### Call Tool (`tools/call`)

```bash
echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_active_alerts","arguments":{"limit":5}}}' | ./bin/mcp
```

---

## 5. Optional HTTP/SSE Mode

For network or containerized setups:

```bash
API_TOKEN=secret123 ./bin/mcp -http 127.0.0.1:8081
```

- Endpoint: `GET /sse` (requires `Authorization: Bearer secret123` if `API_TOKEN` is set).
- Health check: `GET /health` -> `{"status":"ok"}`.
