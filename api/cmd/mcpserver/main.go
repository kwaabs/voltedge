// Command mcpserver is the VoltEdge Model Context Protocol server (Phase 18,
// docs/phase-18-mcp.md). It runs as its own process, beside the REST API,
// and gives an AI agent read-only tools to operate on VoltEdge *data* --
// never on the metadata that governs the system. It shares the API's engine
// packages (internal/crud, internal/metadata) and its trust boundary
// (internal/auth): every MCP call authenticates with an iam.api_tokens
// bearer token that resolves to a real user, and every tool runs under that
// user's RBAC permissions.
//
// Config (all via env, like the other binaries): DATABASE_URL, and MCP_PORT
// (default 8090) for the HTTP listener. The MCP endpoint is POST /mcp;
// /healthz proves DB reachability for orchestration probes.
//
// Mint a token for an agent out-of-band with:
//
//	authadmin mint-token --email agent@example.com --name claude-desktop
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/mcpserver"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	// Install the structured (JSON) handler as slog.Default so the MCP handler's
	// log lines (internal/mcpserver) are machine-parseable like the API's.
	logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	port := os.Getenv("MCP_PORT")
	if port == "" {
		port = "8090"
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.Handler(db))
	mux.HandleFunc("/healthz", healthzHandler(db))

	log.Printf("voltedge mcp server listening on :%s (POST /mcp)", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

// healthzHandler proves the process can reach Postgres, mirroring the API's
// own probe -- a bare 200 would hide a broken DATABASE_URL until the first
// tool call.
func healthzHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		status := "ok"
		code := http.StatusOK
		if err := db.PingContext(ctx); err != nil {
			status = "db unreachable: " + err.Error()
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}
