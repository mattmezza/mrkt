# MCP server

`mrkt mcp serve` runs MCP over stdin/stdout. Configure REST access with `MRKT_URL` and `MRKT_TOKEN`.

- `mrkt_metadata` returns the supported resources, actions, and safety rules without contacting REST.
- `mrkt_query` lists or gets resources and is annotated read-only.
- `mrkt_operate` creates resources or runs explicit actions such as plan, deploy, rollback, pause, resume, cancel, explain, simulate, and replay. It accepts a target, JSON input, and idempotency key.

Results are decoded REST responses returned as structured output. REST authorization remains authoritative. The implementation uses the official `github.com/modelcontextprotocol/go-sdk` v1.7.0 and `mcp.StdioTransport`.
