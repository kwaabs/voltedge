---
name: phase-18-mcp
description: MCP data-plane server — Slice 1 (read-only tools + API-token auth) design and status
metadata:
  type: project
---

Phase 18 builds the MCP (Model Context Protocol) server from the metadata-core.md
§"MCP — data plane only" design: an AI agent operates on VoltEdge *data* via the
same generic metadata-validated engine (internal/crud, internal/metadata) the
REST API uses — never on metadata/schema authoring. Docs: [[docs/phase-18-mcp.md]].

**Locked decisions:** (1) Transport = hand-rolled JSON-RPC 2.0 over MCP
streamable-HTTP, no SDK (matches Phase 10 "minimal audited dependency surface";
also the only statically-verifiable option since [[bash-tool-blocked]]).
(2) Agent identity = per-user opaque bearer tokens in `iam.api_tokens`, reusing
Phase 10 auth primitives so a token flows the identical session→user→roles→
permissions chain and can never exceed its user's RBAC. (3) Tokens minted
out-of-band by `authadmin mint-token` (no online endpoint in Slice 1), matching
Phase 10 "admin pre-provisions, nothing in a seed". (4) Only the 4 genuinely-thin
read tools ship; `suggest_*`/`validate_feature`/`run_trace`/mutating `establish_*`
deferred — they need engine work that doesn't exist yet.

**Slice 1 — code-complete (2026-08-21, verification owed, Bash blocked).**
Migration **0037**_create_api_tokens.sql (NOTE: a stray 0030_create_api_tokens.sql
tombstone exists from a numbering collision — must be deleted from disk; it's an
untracked no-op `SELECT 1;`). Files: api/internal/auth/apitoken.go (Mint/Resolve/
Revoke/ListAPITokens), session.go (loadPermissionsFor shared), context.go
(exported WithUser), crud/search.go (SearchFeatures thin export of listFeatures),
internal/mcpserver/{server.go transport, tools.go 4 tools}, cmd/mcpserver/main.go
(POST /mcp + /healthz, MCP_PORT default 8090 / dev 3705), cmd/authadmin/main.go
(mint-token/list-tokens/revoke-token). Taskfile: mcp-dev, authadmin-{mint,list,
revoke}-token. The 4 tools (all require auth.PermFeaturesRead="features:read"):
list_feature_classes, describe_feature_class, search_features, get_feature.
Tool-level failures reported in-band as callToolResult{IsError:true}; protocol
errors use JSON-RPC codes; 401 (with WWW-Authenticate) before parse if no live
token; notifications→202; batches refused; GET→405.

**Owed verification (user runs):** go build/vet; task migrate (0037, after
deleting stray 0030); mint a token; POST /mcp initialize + notifications/initialized
(202) + tools/list + tools/call each of the 4 with Bearer; 401 without/with
revoked token; isError (not 403) when user lacks features:read.

**Deferred:** suggest_connections/suggest_relationships (ranked-candidate engine),
validate_feature (validate-only entrypoint), run_trace, mutating establish_*/
create_/update_ (+ the still-open user-confirmation model), online token mgmt.
