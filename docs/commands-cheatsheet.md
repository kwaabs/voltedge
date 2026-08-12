# Command cheatsheet — no scripts required

Every command below is a literal one-liner meant to be typed or pasted
directly into whatever interactive terminal you're allowed to run
(PowerShell, cmd, a terminal inside an IDE — doesn't matter which). None
of it executes a saved script file on your host: no `.ps1`, no `.sh`, no
`make`. It only invokes already-installed binaries (`docker`, `go`) with
arguments, which is a different thing to most script-execution security
policies than running a script file.

This is the same set of operations `make` wraps — use whichever path your
environment actually allows. If `make` itself turns out to be fine to run
(it's a compiled binary, not a script), `make help` is shorter to type.

Run `docker compose exec gitbash bash /workspace/...` lines from the repo
root, same as the `docker compose up`/`build` ones.

## Infra

Start the backing services (not the api container):
```
docker compose up -d postgres martin rustfs gitbash
```

Stop everything, keep data:
```
docker compose down
```

Stop everything AND delete volumes — wipes the local DB, destructive:
```
docker compose down -v
```

Status / logs:
```
docker compose ps
docker compose logs -f
```

Start everything, api included, building images as needed:
```
docker compose up -d --build
```

## API (runs on your host, needs Go installed locally)

Dev run against dockerized postgres:
```
cd api
set DATABASE_URL=postgres://postgres:voltedge_dev@localhost:3700/postgres?sslmode=disable
set PORT=3704
go run ./cmd/server
```
(PowerShell: use `$env:DATABASE_URL = "..."` and `$env:PORT = "3704"`
instead of `set`.)

Build / test / tidy / vet / fmt:
```
cd api
go build -o bin/server ./cmd/server
go test ./...
go mod tidy
go vet ./...
gofmt -l -w .
```

Compile a feature class's metadata into physical schema (`gis.<code>`) --
runs on your host like `api-dev`, needs a feature class to already exist
in `metadata.feature_classes` (see the seed example below):
```
cd api
$env:DATABASE_URL = "postgres://postgres:voltedge_dev@localhost:3700/postgres?sslmode=disable"
go run ./cmd/compile POLE
```

## Database — all routed through the gitbash container, never your host

Load the POLE example metadata (`seed/pole_example.sql`) to have
something to compile:
```
docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -f /workspace/seed/pole_example.sql
```

Apply pending migrations:
```
docker compose exec -T -e "DATABASE_URL=postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" gitbash bash /workspace/migrations/apply.sh
```

Show what's been applied:
```
docker compose exec -T gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable" -c "SELECT filename, status, applied_at FROM ops.migration_log ORDER BY id;"
```

Interactive psql shell:
```
docker compose exec gitbash psql "postgres://postgres:voltedge_dev@postgres:5432/postgres?sslmode=disable"
```

Interactive shell in the sidecar itself (git, bash, psql all live here):
```
docker compose exec gitbash bash
```

## Frontend (Phase 3+, not scaffolded yet)

```
cd web
npm run dev
```

## Notes

- These assume the default values from `.env.example`
  (`POSTGRES_PASSWORD=voltedge_dev`, `POSTGRES_DB=postgres`,
  `POSTGRES_PORT=3700`). If you changed `.env`, substitute your own
  values into the connection strings above.
- Inside the `gitbash` container, Postgres is reachable at `postgres:5432`
  (the Docker Compose service name and its *container-internal* port,
  which never changes). From your host directly (the `api` block above),
  it's `localhost:3700` — the published port, deliberately non-default.
- `?sslmode=disable` is required on every connection string here — this
  postgres image has no TLS configured, and the Go client defaults to
  attempting it anyway, failing with `SSL is not enabled on the server`
  if it's missing. Local-dev-only; drop it for any DSN pointed at a real
  deployment with TLS.

## Troubleshooting

**`make` fails with `error launching git: Access is denied`, on *any*
target, including ones that never mention git** (e.g. a plain
`go run ...` recipe) — this isn't your command failing, it's `make`
itself failing to launch its configured shell. On Windows, `make.exe`
commonly resolves its shell through a Git-for-Windows-provided `sh.exe`,
whose launch path goes through `git.exe` internally. If your environment
blocks script/shell execution (the same class of policy that blocks
`.ps1` files), that shell launch gets denied before `make` ever reaches
`docker` or `go` — the error just happens to mention git because of how
the shell resolves, not because your command needed it.

Fix: stop using `make` and run the equivalent command from this
cheatsheet directly in PowerShell/cmd instead. `docker.exe` and `go.exe`
are native binaries invoked directly with no intermediate shell, so this
failure mode doesn't apply to them.
