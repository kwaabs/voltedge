// Command authadmin is the out-of-band account tool for Phase 10 auth
// (docs/phase-10-auth.md) -- the "last resort beneath the floor." It
// talks straight to the database (as whoever holds DATABASE_URL),
// independent of the running API and independent of the local-auth
// switch, so it can:
//
//   - bootstrap the very first break-glass admin on a fresh install
//     (no credentials are ever shipped in a seed), and
//   - reset a lost break-glass password when the UI can't help because
//     you can't log in to fix login.
//
// The password is read from the AUTHADMIN_PASSWORD environment variable,
// never a flag -- so it stays out of shell history and the process
// argument list.
//
// Usage:
//
//	AUTHADMIN_PASSWORD=... authadmin create --email a@b.com --name "Jane" [--break-glass=false] [--role admin]
//	AUTHADMIN_PASSWORD=... authadmin set-password --email a@b.com
//	authadmin grant-role  --email a@b.com --role editor
//	authadmin revoke-role --email a@b.com --role editor
//	authadmin roles
//	authadmin list
//
// `create` makes a break-glass admin by default (that's the bootstrap
// case) and assigns the `admin` role so the account actually has
// permissions -- an account with no role can authenticate but is authorized
// for nothing. Pass --break-glass=false for an ordinary account, --role to
// choose a different role, or --role="" to assign none.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
	}

	db := openDB()
	defer db.Close()
	ctx := context.Background()

	switch os.Args[1] {
	case "create":
		runCreate(ctx, db, os.Args[2:])
	case "set-password":
		runSetPassword(ctx, db, os.Args[2:])
	case "grant-role":
		runGrantRole(ctx, db, os.Args[2:])
	case "revoke-role":
		runRevokeRole(ctx, db, os.Args[2:])
	case "roles":
		runRoles(ctx, db)
	case "list":
		runList(ctx, db)
	case "mint-token":
		runMintToken(ctx, db, os.Args[2:])
	case "list-tokens":
		runListTokens(ctx, db, os.Args[2:])
	case "revoke-token":
		runRevokeToken(ctx, db, os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	log.Fatal("usage: authadmin <create|set-password|grant-role|revoke-role|roles|list|mint-token|list-tokens|revoke-token> [flags]\n" +
		"  create        --email <e> --name <n> [--break-glass=true] [--role admin]  (password from AUTHADMIN_PASSWORD)\n" +
		"  set-password  --email <e>                                                  (password from AUTHADMIN_PASSWORD)\n" +
		"  grant-role    --email <e> --role <r>\n" +
		"  revoke-role   --email <e> --role <r>\n" +
		"  roles                                                                      list the available roles\n" +
		"  list                                                                       list users and their roles\n" +
		"  mint-token    --email <e> --name <n> [--ttl <dur>]                         mint an MCP API token (printed once)\n" +
		"  list-tokens   --email <e>                                                  list a user's MCP API tokens\n" +
		"  revoke-token  --email <e> --id <token-id>                                  revoke one MCP API token")
}

func openDB() *bun.DB {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	return bun.NewDB(sqldb, pgdialect.New())
}

func password() string {
	pw := os.Getenv("AUTHADMIN_PASSWORD")
	if pw == "" {
		log.Fatal("AUTHADMIN_PASSWORD is not set (the password is read from the environment, not a flag)")
	}
	return pw
}

func runCreate(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	email := fs.String("email", "", "account email (also the local login identity)")
	name := fs.String("name", "", "display name")
	breakGlass := fs.Bool("break-glass", true, "mark this account break-glass (bypasses the local-auth switch)")
	role := fs.String("role", "admin", "role to assign on creation (empty to assign none)")
	_ = fs.Parse(args)

	if *email == "" || *name == "" {
		log.Fatal("create: --email and --name are required")
	}

	id, err := auth.CreateUser(ctx, db, *email, *name, password(), *breakGlass)
	if err != nil {
		log.Fatalf("create: %v", err)
	}
	kind := "user"
	if *breakGlass {
		kind = "break-glass admin"
	}
	fmt.Printf("created %s %s (%s)\n", kind, *email, id)

	// Assign the role so the account is authorized, not just authenticated.
	// A bootstrap admin with no role can sign in but is forbidden every
	// route, which looks like a broken install.
	if *role != "" {
		if err := auth.AssignRole(ctx, db, *email, *role, ""); err != nil {
			log.Fatalf("create: account made but role %q not assigned: %v", *role, err)
		}
		fmt.Printf("assigned role %s to %s\n", *role, *email)
	}
}

func runGrantRole(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("grant-role", flag.ExitOnError)
	email := fs.String("email", "", "account email")
	role := fs.String("role", "", "role key (see `authadmin roles`)")
	_ = fs.Parse(args)

	if *email == "" || *role == "" {
		log.Fatal("grant-role: --email and --role are required")
	}
	if err := auth.AssignRole(ctx, db, *email, *role, ""); err != nil {
		log.Fatalf("grant-role: %v", err)
	}
	fmt.Printf("granted role %s to %s\n", *role, *email)
}

func runRevokeRole(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("revoke-role", flag.ExitOnError)
	email := fs.String("email", "", "account email")
	role := fs.String("role", "", "role key (see `authadmin roles`)")
	_ = fs.Parse(args)

	if *email == "" || *role == "" {
		log.Fatal("revoke-role: --email and --role are required")
	}
	if err := auth.RemoveRole(ctx, db, *email, *role); err != nil {
		log.Fatalf("revoke-role: %v", err)
	}
	fmt.Printf("revoked role %s from %s\n", *role, *email)
}

func runRoles(ctx context.Context, db *bun.DB) {
	roles, err := auth.ListRoles(ctx, db)
	if err != nil {
		log.Fatalf("roles: %v", err)
	}
	for _, r := range roles {
		fmt.Printf("%-18s  %-18s  %s\n", r.Key, r.Name, r.Description)
	}
}

func runSetPassword(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	email := fs.String("email", "", "account email")
	_ = fs.Parse(args)

	if *email == "" {
		log.Fatal("set-password: --email is required")
	}
	if err := auth.SetLocalPassword(ctx, db, *email, password()); err != nil {
		log.Fatalf("set-password: %v", err)
	}
	fmt.Printf("password reset for %s\n", *email)
}

func runList(ctx context.Context, db *bun.DB) {
	users, err := auth.ListUsers(ctx, db)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	if len(users) == 0 {
		fmt.Println("(no users yet)")
		return
	}
	for _, u := range users {
		flag := ""
		if u.BreakGlass {
			flag = " [break-glass]"
		}
		roles := "-"
		if len(u.Roles) > 0 {
			roles = strings.Join(u.Roles, ",")
		}
		fmt.Printf("%s  %-32s  %-10s  %-20s  %s%s\n", u.ID, u.Email, u.Status, roles, u.DisplayName, flag)
	}
}

func runMintToken(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("mint-token", flag.ExitOnError)
	email := fs.String("email", "", "owning account email")
	name := fs.String("name", "", "human label for the token (e.g. claude-desktop)")
	ttl := fs.Duration("ttl", 0, "optional lifetime (e.g. 720h); omit or 0 for no expiry")
	_ = fs.Parse(args)

	if *email == "" || *name == "" {
		log.Fatal("mint-token: --email and --name are required")
	}

	// createdBy is "" here: this is an out-of-band mint with no acting user,
	// the same path that bootstraps the first admin.
	raw, tok, err := auth.MintAPIToken(ctx, db, *email, *name, *ttl, "")
	if err != nil {
		log.Fatalf("mint-token: %v", err)
	}

	exp := "never"
	if tok.ExpiresAt != nil {
		exp = tok.ExpiresAt.Format(time.RFC3339)
	}
	fmt.Printf("minted token %s (%s) for %s, expires %s\n", tok.ID, tok.Name, *email, exp)
	fmt.Println("--- copy this token now; it is shown only once and cannot be recovered ---")
	fmt.Println(raw)
}

func runListTokens(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("list-tokens", flag.ExitOnError)
	email := fs.String("email", "", "owning account email")
	_ = fs.Parse(args)

	if *email == "" {
		log.Fatal("list-tokens: --email is required")
	}
	toks, err := auth.ListAPITokens(ctx, db, *email)
	if err != nil {
		log.Fatalf("list-tokens: %v", err)
	}
	if len(toks) == 0 {
		fmt.Println("(no tokens)")
		return
	}
	for _, t := range toks {
		state := "active"
		switch {
		case t.RevokedAt != nil:
			state = "revoked " + t.RevokedAt.Format(time.RFC3339)
		case t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt):
			state = "expired " + t.ExpiresAt.Format(time.RFC3339)
		case t.ExpiresAt != nil:
			state = "expires " + t.ExpiresAt.Format(time.RFC3339)
		}
		used := "never"
		if t.LastUsedAt != nil {
			used = t.LastUsedAt.Format(time.RFC3339)
		}
		fmt.Printf("%s  %-20s  %-14s  created %s  last-used %s  %s\n",
			t.ID, t.Name, t.TokenPrefix, t.CreatedAt.Format(time.RFC3339), used, state)
	}
}

func runRevokeToken(ctx context.Context, db *bun.DB, args []string) {
	fs := flag.NewFlagSet("revoke-token", flag.ExitOnError)
	email := fs.String("email", "", "owning account email")
	id := fs.String("id", "", "token id (see `authadmin list-tokens`)")
	_ = fs.Parse(args)

	if *email == "" || *id == "" {
		log.Fatal("revoke-token: --email and --id are required")
	}
	n, err := auth.RevokeAPIToken(ctx, db, *email, *id)
	if err != nil {
		log.Fatalf("revoke-token: %v", err)
	}
	if n == 0 {
		fmt.Printf("no live token %s for %s (already revoked, or no such id)\n", *id, *email)
		return
	}
	fmt.Printf("revoked token %s for %s\n", *id, *email)
}
