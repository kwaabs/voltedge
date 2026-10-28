// Command server is the VoltEdge API. Phase 0 gave it a healthcheck;
// Phase 2 adds the generic, metadata-driven CRUD engine (api/internal/crud)
// -- one set of handlers that serves every compiled feature class, no
// feature-specific code required -- plus two explicit-confirm engines:
// zero-trust electrical connectivity (api/internal/connectivity) and
// structural/spatial relationships (api/internal/relationship), the
// only paths that turn a candidate connection or relationship into
// real topology/fact. Phase 4 adds the read-only network trace engine
// (api/internal/trace) that walks that same committed topology.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kwaabs/voltedge/api/internal/admin"
	"github.com/kwaabs/voltedge/api/internal/appdist"
	"github.com/kwaabs/voltedge/api/internal/assembly"
	"github.com/kwaabs/voltedge/api/internal/assetlife"
	"github.com/kwaabs/voltedge/api/internal/assettag"
	"github.com/kwaabs/voltedge/api/internal/assistant"
	"github.com/kwaabs/voltedge/api/internal/assistant/llm"
	"github.com/kwaabs/voltedge/api/internal/attachment"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/basemap"
	"github.com/kwaabs/voltedge/api/internal/boq"
	"github.com/kwaabs/voltedge/api/internal/circuit"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/coupler"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/kwaabs/voltedge/api/internal/diagram"
	"github.com/kwaabs/voltedge/api/internal/docs"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/duct"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/externallink"
	"github.com/kwaabs/voltedge/api/internal/externalsystem"
	"github.com/kwaabs/voltedge/api/internal/faults"
	"github.com/kwaabs/voltedge/api/internal/featureregistry"
	"github.com/kwaabs/voltedge/api/internal/fieldpack"
	"github.com/kwaabs/voltedge/api/internal/fieldtrack"
	"github.com/kwaabs/voltedge/api/internal/httpx"
	"github.com/kwaabs/voltedge/api/internal/importer"
	"github.com/kwaabs/voltedge/api/internal/issuework"
	"github.com/kwaabs/voltedge/api/internal/logsettings"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/mapannotation"
	"github.com/kwaabs/voltedge/api/internal/mapsettings"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/metermount"
	"github.com/kwaabs/voltedge/api/internal/oms"
	"github.com/kwaabs/voltedge/api/internal/ownership"
	"github.com/kwaabs/voltedge/api/internal/packagedsub"
	"github.com/kwaabs/voltedge/api/internal/panel"
	"github.com/kwaabs/voltedge/api/internal/powerflow"
	"github.com/kwaabs/voltedge/api/internal/ratelimit"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/kwaabs/voltedge/api/internal/rmu"
	"github.com/kwaabs/voltedge/api/internal/rules"
	"github.com/kwaabs/voltedge/api/internal/safety"
	"github.com/kwaabs/voltedge/api/internal/schematic"
	"github.com/kwaabs/voltedge/api/internal/spatial"
	"github.com/kwaabs/voltedge/api/internal/streetlight"
	"github.com/kwaabs/voltedge/api/internal/studies"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/kwaabs/voltedge/api/internal/servicelayers"
	"github.com/kwaabs/voltedge/api/internal/telemetry"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/kwaabs/voltedge/api/internal/tie"
	"github.com/kwaabs/voltedge/api/internal/tiles"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/kwaabs/voltedge/api/internal/webhook"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func main() {
	// The structured-logging root. Built first so everything below (and any
	// slog.Default call) shares one format/level/destination. Boot-time fatals
	// below still use stdlib log.Fatal -- they abort before the server exists.
	rootLogger := logx.Init()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL)))
	db := bun.NewDB(sqldb, pgdialect.New())
	defer db.Close()

	// Re-apply the admin-set runtime log level (internal/logsettings), if one was
	// saved, OVER the env default -- so a level an operator chose in the UI
	// survives a restart. A missing row keeps the env/default level; a read error
	// is non-fatal (logging verbosity is never worth failing boot over).
	if lvl, ok, lerr := logsettings.LoadPersistedLevel(context.Background(), db); lerr != nil {
		rootLogger.Warn("could not read persisted log level", "err", lerr)
	} else if ok {
		logx.SetLevel(lvl)
	}

	// Phase 17: export artifacts stream to RustFS and download via presigned
	// URL. Building the client is offline (config only), so a momentarily
	// unreachable RustFS does not stop boot; only ensuring the bucket touches
	// the network, and that failure is a warning -- exports will fail with a
	// clear per-job error rather than taking the whole API down.
	exportStore, err := export.NewObjectStoreFromEnv()
	if err != nil {
		log.Fatalf("export object store: %v", err)
	}
	if err := exportStore.EnsureBucket(context.Background()); err != nil {
		log.Printf("warning: could not ensure export bucket (exports fail until RustFS is reachable): %v", err)
	}

	// Field-app distribution ("our own play store"): a separate bucket for the
	// published signed APK + its latest.json manifest, served publicly (see the
	// /app mount below). A nil store (RustFS misconfigured) just disables the
	// endpoints; it never blocks boot.
	appStore, appStoreErr := export.NewObjectStoreForBucket(appdist.Bucket)
	if appStoreErr != nil {
		log.Printf("warning: app-distribution store unavailable (field-app download disabled): %v", appStoreErr)
	} else if err := appStore.EnsureBucket(context.Background()); err != nil {
		log.Printf("warning: could not ensure app bucket (field-app download fails until RustFS is reachable): %v", err)
	}

	// Phase 19 Slice 3: the assistant chat orchestrator's model. Optional --
	// unset ASSISTANT_LLM_BASE_URL yields a nil provider, so the API boots fine
	// with chat disabled (the deterministic suggestions still work); a non-nil
	// error means a variable that *is* set is malformed, worth failing on.
	llmProvider, err := llm.NewFromEnv()
	if err != nil {
		log.Fatalf("assistant llm: %v", err)
	}
	if llmProvider == nil {
		log.Print("assistant chat disabled (ASSISTANT_LLM_BASE_URL unset)")
	} else {
		log.Printf("assistant chat enabled (%s)", llmProvider.Name())
	}

	// Phase 32 Slice 2: at-rest encryption for external-system credentials.
	// Optional, like the llm provider -- a nil cipher (EXTERNAL_SYSTEMS_
	// ENCRYPTION_KEY unset) still boots; the catalog then refuses to store a
	// secret rather than keeping it in the clear. A key that is set but
	// malformed is a hard boot error.
	credCipher, err := secretbox.NewFromEnv()
	if err != nil {
		log.Fatalf("external-systems encryption: %v", err)
	}
	if credCipher == nil {
		log.Printf("external-system credential encryption disabled (%s unset); systems with auth can't be saved until it's set", secretbox.EnvKey)
	}

	r := chi.NewRouter()
	// Structured request logging (internal/httpx + logx) in place of chi's
	// plain-text middleware.Logger: one JSON summary line per request, tagged
	// with a correlation id (also returned as X-Request-Id) and -- once the
	// session middleware below resolves the caller -- the acting user.
	r.Use(httpx.Logger(rootLogger))
	r.Use(middleware.Recoverer)
	// A generous global request-body cap: a backstop so no endpoint can be made to
	// buffer an unbounded body. It is sized well above any legitimate JSON control
	// payload (bulk feature bytes ride presigned PUT straight to the object store,
	// never this process); endpoints with known-small bodies add tighter caps of
	// their own (see auth.Routes).
	r.Use(httpx.MaxBytes(32 << 20))

	// Phase 10: the trust boundary. SecurityHeaders hardens every response;
	// SessionLoader validates the session cookie (if any) and attaches the
	// authenticated user -- and their effective permission set -- to the
	// request context. SessionLoader itself is non-failing (an anonymous
	// request flows through); it is the per-route RBAC gates below (Slice C)
	// that actually reject: 401 without a session, 403 without the permission.
	authCfg := auth.NewConfigFromEnv()
	r.Use(auth.SecurityHeaders())
	r.Use(auth.SessionLoader(db, authCfg))
	// Edit-session tagging: when the web client is in edit mode it sends an
	// X-Edit-Session uuid on each mutating request; this stamps it onto the
	// request context so the CRUD history writer can group a batch of edits.
	r.Use(auth.EditSessionLoader())
	// Enrich the request-summary log line with the resolved caller. This runs
	// just after SessionLoader so UserFrom sees the attached user; it writes into
	// the pointer-carried logx attribute bag the request logger opened upstream,
	// so the id lands on that one summary line. Only the opaque user id is logged
	// (not email/display name) -- logs are an audit-grade trail, keep PII out.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u, ok := auth.UserFrom(r.Context()); ok {
				logx.AddAttr(r.Context(), slog.String("user.id", u.ID))
			}
			next.ServeHTTP(w, r)
		})
	})

	// /healthz stays public -- container orchestration probes it with no
	// session, and it exposes nothing but DB reachability.
	r.Get("/healthz", healthzHandler(db))

	// Inbound webhook receiver (0124, internal/webhook): the PUBLIC ingestion
	// surface external systems push feature (later reading/work/fault) changes to.
	// It sits OUTSIDE the session/RBAC stack on purpose -- each endpoint carries
	// its own per-endpoint credential (HMAC signature and/or bearer token verified
	// in the handler), and every applied write still runs as the endpoint's
	// configured service user, so it can never exceed that user's permissions or
	// territory scope. A coarse per-IP limiter (the login limiter's cousin) and a
	// tight 1 MiB body cap wrap it, since an unauthenticated caller reaches it: the
	// receiver only authenticates + enqueues, so payloads are control-sized, not
	// bulk. The out-of-process cmd/webhookworker drains the queue and applies.
	webhookLimiter := ratelimit.New(300, 100)
	r.With(webhookLimiter.Middleware, httpx.MaxBytes(1<<20)).Mount("/webhooks", webhook.ReceiverRoutes(db, credCipher))

	// The map/forms read layer: the feature-class catalog, per-class schema
	// and rules, and the global symbology icon set. All GET, all gated by
	// features:read -- any viewer must load these to see the map at all.
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead))
		r.Get("/feature-classes", featureClassesHandler(db))
		r.Get("/feature-classes/{code}/schema", featureClassSchemaHandler(db))
		r.Get("/feature-classes/{code}/rules", rules.Handler(db))
		r.Get("/symbology/icons", symbologyIconsHandler(db))
		r.Get("/symbology/stencils", diagramStencilsHandler(db))
	})

	// Domain mounts, each wrapped in an RBAC gate. Mixed read/write mounts
	// get a method-aware gate (GET => the :read permission, mutating => the
	// :write/:commit one); the pure-action engines (trace, analysis), whose
	// POSTs are queries rather than writes, get a single permission for
	// every method. /auth is not wrapped here -- it self-gates (login and
	// the SSO endpoints are public by necessity; logout/me/settings carry
	// their own RequireAuth/RequireCSRF, and the switch checks
	// admin:auth-settings/break-glass itself).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/features", crud.Routes(db))
	// ETL import engine (docs/etl-import-engine.md): stage an uploaded source
	// artifact and pre-flight a mapping (S3), then run it (S4). Running an
	// import writes features through the same validated crud path, so it rides
	// the features:read/features:write gate -- authoring the mapping profile is
	// separate metadata:write config under /admin.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/imports", importer.Routes(db, exportStore, credCipher))
	// Geographic export (Phase 14): reads features (optionally clipped to the
	// map viewport) and streams them back as a KMZ/Shapefile/DXF download. A
	// read, like trace/analysis -- POST only because the filter and scope don't
	// fit query params -- so a single features:read gate covers every method.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/export", export.Routes(db, exportStore))
	// Vector tiles (Phase 58 Route A): the map's per-class MVT layers and the
	// catalog that lists them. Replaces Martin as the /tiles origin so tiles are
	// generated in-process behind the session -- a scoped user is never sent the
	// geometry of a feature outside their territories. GET-only, so features:read
	// covers every method, the same gate the feature-class read layer above uses.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/tiles", tiles.Routes(db))
	// The map's on-screen "Schematic view" (operations one-line) data: a read-only,
	// viewport-scoped projection of the same features the map shows -- nodes +
	// connectivity edges for a bbox, with the S1 overlay attributes. GET-only, and
	// it applies the SAME territory + private-draft scope the tiles do (see
	// schematic.resolveScope), so features:read covers every method.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/schematic", schematic.Routes(db))
	// Territories are fixed reference data read alongside features (the
	// feature panel resolves a feature's territory_id to a name via this
	// list), so the read-only endpoint sits under the same features:read
	// gate every map viewer already holds -- not the metadata admin gate.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermTerritoriesWrite)).Mount("/territories", territory.Routes(db))
	// Circuits (Phase 37): the catalog of named, network-wide electrical
	// circuits (e.g. the MV feeder "Old Age 2") many features across many
	// classes belong to. Like territories, it's reference data read alongside
	// features (a feature's circuit_id resolves to a circuit name here), so the
	// read side rides features:read while curation needs circuits:write.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermCircuitsWrite)).Mount("/circuits", circuit.Routes(db))
	// Asset-ownership propagation (internal/ownership): declare a transformer
	// private and stamp ownership=private onto everything downstream, plus a
	// global re-sync. Both POSTs bulk-write feature rows, so the mount requires
	// features:write -- the same capability a single attribute edit needs.
	r.With(auth.RequirePermission(auth.PermFeaturesWrite, auth.PermFeaturesWrite)).Mount("/ownership", ownership.Routes(db))
	// Outage Management (Phase 49): the outage board -- trouble-call intake,
	// call-to-device prediction, and incident lifecycle. Its own oms:read/
	// oms:write capability (an outage is an operational transaction, not feature
	// data), gated method-aware here so this router carries no auth of its own.
	r.With(auth.RequirePermission(auth.PermOMSRead, auth.PermOMSWrite)).Mount("/oms", oms.Routes(db))
	// Phase 59: Work & Asset Management (WAM) -- asset defects and the one typed
	// work order engine (inspection/corrective/preventive/construction), with the
	// defect->corrective-order and defect<-OMS-incident links. Its own wam:read/
	// wam:write capability (a defect or work order is an operational record about
	// the assets, not feature data), gated method-aware here so this router
	// carries no auth of its own -- the same arrangement OMS uses.
	r.With(auth.RequirePermission(auth.PermWAMRead, auth.PermWAMWrite)).Mount("/wam", wam.Routes(db))
	// Faults: the electrical fault event -- the causal hinge between OMS (the
	// outage a fault causes) and WAM (the defect a fault triggers). Its own
	// faults:read/faults:write capability (a fault straddles both subsystems but
	// is neither an outage nor an asset-condition record), gated method-aware here
	// so this router carries no auth of its own -- the same arrangement OMS/WAM use.
	r.With(auth.RequirePermission(auth.PermFaultsRead, auth.PermFaultsWrite)).Mount("/faults", faults.Routes(db))
	// Device-operation event log (migration 0147): the append-only record of
	// protective/switching device operations (trip/lockout/reclose/open/close).
	// Its own operations:* capability (a device operation is neither a fault
	// investigation nor an outage); method-aware gate, router carries no auth.
	r.With(auth.RequirePermission(auth.PermOperationsRead, auth.PermOperationsWrite)).Mount("/operations", deviceops.Routes(db))
	// Operational safety (migration 0151): tags, grounds and clearances, and the
	// interlock a blocking tag feeds. Its own safety:* capability -- a blocking tag
	// bars operation for everyone, so the right to protect (and release) a work area
	// is granted deliberately, not folded into switching rights. Method-aware gate.
	r.With(auth.RequirePermission(auth.PermSafetyRead, auth.PermSafetyWrite)).Mount("/safety", safety.Routes(db))
	// Live-state / telemetry ingestion (migration 0172, blocker #1): a latest-value
	// device-telemetry store fed by a dedicated lightweight endpoint (NOT the
	// durable webhook event engine -- telemetry is a high-rate, latest-value,
	// loss-tolerant stream), reconciled against the model device_state. Routes
	// self-gate per permission (telemetry:read / telemetry:ingest / oms:write for
	// apply), so this is a plain mount; the SCADA/DMS gateway that feeds it is external.
	r.Mount("/telemetry", telemetry.Routes(db))
	// Field breadcrumb audit trail (migration 0178): the supervisor read map over
	// where field workers physically were -- their movement trails and the located
	// stamp on every mutating edit. Read-only and privacy-sensitive, so its own
	// field:tracks:read capability gates the whole mount (all routes are reads).
	// The field user PUSHES their own trail under features:write on the fieldsync
	// surface; seeing someone else's is the privileged action gated here.
	r.With(auth.RequirePermission(auth.PermFieldTracksRead, auth.PermFieldTracksRead)).Mount("/field-tracks", fieldtrack.Routes(db))
	// Spatial engine (geometry-derived overlap/proximity across the gis.* tables):
	// "what assets are inside this easement / corridor / parcel?", "what land covers
	// this asset?", "what vegetation is within N m of this line?". Read-only over the
	// features themselves, so it rides features:read like the feature registry.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/spatial", spatial.Routes(db))
	// Assign a data-quality issue as a fix: raise a WAM work order from the issue
	// and link it, so the fix lands on an office or field user's board (and syncs
	// into their field pack). Gated wam:write in-router; the issue's acknowledge is
	// a side effect of the assignment. Thin orchestration over connectivity + wam.
	r.With(auth.RequirePermission(auth.PermWAMWrite, auth.PermWAMWrite)).Mount("/validation-issues", issuework.Routes(db))
	// Feature registry (migration 0108): a cross-class index of every feature
	// id -> its class, maintained by the compiler-attached DB trigger. Backs
	// the asset-id typeahead/resolve any form needs when a user references an
	// asset by id (faults/oms/wam and the connectivity forms are the first
	// consumers). Read-only and just an index of the features themselves, so it
	// rides the same features:read gate the CRUD read layer uses -- GET-only, so
	// features:read covers every method (the /tiles / /export arrangement).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/feature-registry", featureregistry.Routes(db))
	// Asset tagging (migrations 0114/0115): every feature carries scannable
	// tag codes (QR + Code128 + human-readable) affixed on the physical asset;
	// a scan resolves the code back to the feature. Codes are MINTED by a DB
	// trigger on runtime.feature_registry (no endpoint mints), so this router is
	// the read + lifecycle side: resolve (scope/draft-aware, so a code you can't
	// see 404s), list a feature's tags, register a legacy number, set primary,
	// void, reissue. Its own tags:read/tags:write capability -- a resolve is a
	// field read anyone can do, tag lifecycle is an editor act, neither being a
	// feature edit -- gated method-aware here, the oms/wam/faults arrangement.
	r.With(auth.RequirePermission(auth.PermTagsRead, auth.PermTagsWrite)).Mount("/tags", assettag.Routes(db))
	// Phase 22: files attached to features (docs/phase-22-attachments.md). A
	// method-aware attachments:read/write gate covers the mount; the bytes ride
	// presigned URLs straight to/from RustFS (exportStore's bucket), so this
	// process only issues URLs and records metadata. Scoped per feature by
	// (class code, feature id), the same addressing the CRUD engine uses.
	r.With(auth.RequirePermission(auth.PermAttachmentsRead, auth.PermAttachmentsWrite)).Mount("/attachments", attachment.Routes(db, exportStore))
	r.With(auth.RequirePermission(auth.PermConnectivityRead, auth.PermConnectivityCommit)).Mount("/connectivity", connectivity.Routes(db))
	r.With(auth.RequirePermission(auth.PermRelationshipsRead, auth.PermRelationshipsCommit)).Mount("/relationships", relationship.Routes(db))
	// Phase 42 (indoor MV switchgear): the composite "place one switchgear panel
	// and get its whole standard device set" endpoint. It creates
	// features and writes containment relationships in one transaction, so it needs
	// BOTH capabilities: the method-aware features gate here covers the feature
	// writes, and panel.Routes adds an inner relationships:commit requirement --
	// the same layered two-capability gate circuit's assign route uses.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/panels", panel.Routes(db))
	// Phase RMU (docs/phase-rmu.md): the composite "place one Ring Main Unit and
	// get its whole assembly of ways + internal bus" endpoint. Like /panels it
	// creates features and writes containment, but it ALSO commits the internal
	// way -> bus connections, so it needs THREE capabilities: the method-aware
	// features gate here covers the feature writes, and rmu.Routes adds inner
	// relationships:commit + connectivity:commit requirements -- the panel gate,
	// one capability deeper because the RMU is the first fan-out wiring topology.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/rmus", rmu.Routes(db))
	// Phase 56 (docs/phase-56-packaged-substation.md): the composite "place one
	// packaged (movable) substation and get its whole pre-wired kit" endpoint. Like
	// /rmus it creates features, writes containment AND commits internal
	// connectivity, so it needs the same THREE capabilities: the method-aware
	// features gate here covers the feature writes, and packagedsub.Routes adds the
	// inner relationships:commit + connectivity:commit requirements. The only
	// difference from /rmus is topology -- a chain (switch -> transformer -> board)
	// rather than a star of ways around one bus.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/packaged-substations", packagedsub.Routes(db))
	// Phase 57 (docs/phase-57-overhead-urd-assemblies.md): the GENERAL kit-assembly
	// endpoint -- place one anchor feature and fan out its templated kit (the pole-
	// mounted overhead transformer, the pad-mount URD unit, and future recloser/
	// capacitor/BESS kits). It generalises /rmus and /packaged-substations: each item
	// names its own structural parent + relationship kind AND its electrical wiring
	// target by position, so one engine describes a star, a chain OR a branch. Like
	// them it creates features, writes relationships AND commits connectivity, so it
	// carries the same THREE capabilities (features gate here + inner relationships:
	// commit + connectivity:commit in assembly.Routes).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/assemblies", assembly.Routes(db))
	// Attach a lamp to a pole: create a geometry-less pole-mounted STREET_LIGHT,
	// link it to the pole, and best-effort auto-wire it to the pole's nearby LV.
	// Features gate here; relationships:commit (always) + connectivity:commit (for
	// the auto-wire) are enforced inside streetlight.Routes.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/street-lights", streetlight.Routes(db))
	// Attach a METER to a SERVICE POINT (seed/meter_service_point_mounted.sql): a
	// geometry-less meter that rides its SP, wired METER T2 -> SERVICE_POINT T1.
	// Same gate shape as /street-lights -- features here, relationships:commit +
	// connectivity:commit (for the wire) inside metermount.Routes.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/meter-mounts", metermount.Routes(db))
	// Phase 44 Slice 4 (docs/phase-44-cable-management.md): the composite "create one
	// cable duct and fan out its ways" endpoint. Like /panels it creates features and
	// writes containment (duct -> each DUCT_WAY) in one transaction, so it needs BOTH
	// capabilities: the method-aware features gate here covers the feature writes, and
	// duct.Routes adds an inner relationships:commit requirement. Unlike /rmus it
	// commits no connectivity -- duct ways are network_role='none', never traced.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/ducts", duct.Routes(db))
	// Phase Bus-coupler Slice 2 (docs/phase-bus-coupler.md): the composite "place
	// one bus coupler / bus tie" endpoint -- create an MV_BREAKER and wire it
	// between two named buses (busbar rules C + D) in one transaction. It creates
	// a feature and commits two connections (but no containment relationship), so
	// the method-aware features gate here covers the create, and coupler.Routes
	// adds an inner connectivity:commit requirement -- the /rmus gate, one
	// capability shallower (no relationships:commit: a coupler wires no assembly).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/couplers", coupler.Routes(db))
	// Phase 50 Slice 2 Part A (docs/phase-50-advanced-analysis.md): the composite
	// "create one feeder-to-feeder tie" endpoint -- create an MV_SWITCH and wire it
	// between two named conductors (switch rules 1 + 3) in one transaction, born
	// normal_state=NORMALLY_OPEN + device_state=OPEN so it is a standby (NOP) path.
	// Same shape as /couplers: creates a feature and commits two connections (no
	// containment), so the method-aware features gate here covers the create and
	// tie.Routes adds an inner connectivity:commit requirement. The N-1 contingency
	// analysis (Part B) is these ties' first engine consumer.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/ties", tie.Routes(db))
	// Phase 32 (docs/phase-32-external-links.md): outbound links from a feature
	// to external systems. Slice 1 is plain hyperlinks. Reading/writing a
	// feature's links rides the same features:read/features:write gate every map
	// user already holds -- a link is feature metadata, not its own capability.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesWrite)).Mount("/feature-links", externallink.Routes(db, credCipher))
	r.With(auth.RequirePermission(auth.PermDiagramsRead, auth.PermDiagramsWrite)).Mount("/diagrams", diagram.Routes(db))
	// Phase 36 (docs/phase-36-map-annotations.md): the map's redlining layer --
	// free-form text/box/line/arrow markup drawn straight on the geographic map,
	// each individually styled. Furniture (the geographic sibling of a diagram
	// element), not a feature, so it gets its own annotations:read/write gate
	// rather than riding features:*. A method-aware gate covers the mixed mount.
	r.With(auth.RequirePermission(auth.PermAnnotationsRead, auth.PermAnnotationsWrite)).Mount("/map-annotations", mapannotation.Routes(db))
	r.With(auth.RequirePermission(auth.PermTraceRun, auth.PermTraceRun)).Mount("/trace", trace.Routes(db))
	r.With(auth.RequirePermission(auth.PermAnalysisRun, auth.PermAnalysisRun)).Mount("/analysis", powerflow.Routes(db))
	// The protection-curve CATALOG + its admin editor. Method-aware gate: GET is
	// features:read (the feature form's fuse/relay curve picker reads it, and those
	// authors may not hold analysis:run), while every write (admin CRUD over curves
	// and their points) is metadata:write (powerflow.CatalogRoutes).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/curves", powerflow.CatalogRoutes(db))
	// Bill of Quantities: a priced schedule over a scope. Read-only, reads feature
	// quantities (like /aggregate) -- gated on features:read.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermFeaturesRead)).Mount("/boq", boq.Routes(db, exportStore))
	// Asset-lifecycle aging: the expected-useful-life catalog + the remaining-life
	// report. Method-aware -- the report/catalog read is features:read, editing the
	// catalog is metadata:write (admin policy, like the cost/curve catalogs).
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/asset-life", assetlife.Routes(db))
	// Phase 19: the in-app AI assistant's backend (docs/phase-19-assistant.md).
	// Only RequireAuth here, not a single RBAC gate -- /assistant/suggestions
	// reads two capabilities at once (connectivity + relationships) and gates
	// each independently on the session user's own commit permission inside the
	// handler, so a user with one but not the other still gets the half they can
	// act on. Every action it proposes is executed through the establish routes
	// above (or the assistant's own /execute), which re-enforce those same commit
	// permissions on the actual write. The Slice 3 chat orchestrator routes over
	// llmProvider, which is nil when no model is configured (ASSISTANT_LLM_BASE_URL
	// unset) -- chat then reports itself disabled while suggestions keep working.
	r.With(auth.RequireAuth).Mount("/assistant", assistant.Routes(db, llmProvider))
	// In-app documentation viewer: the product manuals (docs/*.md, curated by
	// docs/README.md) served to a signed-in user. Only RequireAuth here, not a
	// single RBAC gate -- every authenticated user reaches the mount, and the
	// per-document RequiredPerm inside docs.Routes is what restricts the
	// administrative manuals (the Admin & Break-Glass guide needs admin:users, the
	// CIM export guide needs metadata:read): a caller without the permission never
	// sees the entry in the catalog and is refused 403 on a direct fetch. No db --
	// the manuals are files on disk (DOCS_DIR), not database rows.
	r.With(auth.RequireAuth).Mount("/docs", docs.Routes())
	// The metadata admin catalog (feature classes, domains, field groups,
	// materials, symbology, network/relationship rules, import-profile authoring,
	// CIM mappings, stencils). Both read and write require metadata:write, so the
	// whole catalog is admin-only: a viewer/analyst/editor (who hold metadata:read
	// but not metadata:write) can no longer browse it read-only by typing the URL
	// -- they get a clean 403. The map/forms read layer that non-admins do need
	// lives on the separate features:read /feature-classes mount above.
	r.With(auth.RequirePermission(auth.PermMetadataWrite, auth.PermMetadataWrite)).Mount("/admin", admin.Routes(db, credCipher))
	// Phase 39 (docs/phase-39-map-settings.md): the admin-configurable default
	// map view (center lon/lat + zoom at app start), replacing the client's
	// hardcoded placeholder. A method-aware gate: GET needs only features:read
	// -- every map user fetches the default at startup -- while the PUT that
	// changes it needs metadata:write, the same admin gate as /admin. Its page
	// lives under /admin/map-settings while the API sits top-level, the same
	// split as external-systems and field-packs.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/map-settings", mapsettings.Routes(db))
	// The runtime log-level control (internal/logsettings): raise/lower the
	// server's structured-logging verbosity live, no restart. Gated on admin:users
	// for BOTH read and write -- it is a platform-operations control, not a
	// per-user preference, so unlike map-settings even the GET needs the admin
	// capability. Persisted in iam.system_settings and re-applied at boot above.
	r.With(auth.RequirePermission(auth.PermUsersManage, auth.PermUsersManage)).Mount("/log-settings", logsettings.Routes(db))
	// Phase 61 private-drafts rollout switch (private_drafts_enabled). Same
	// method-aware gate as map-settings: GET is features:read so an admin screen
	// can show the current state, while the PUT that flips it -- retroactively
	// privatizing every never-published feature to its author chain -- needs
	// metadata:write, the admin gate. Mirrors territory's /enforcement toggle.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/private-drafts", drafts.Routes(db))
	// The admin-managed basemap catalog (migration 0077) -- the raster tile
	// sources the geographic map can show, replacing the client's hardcoded
	// list. Same method-aware gate as map-settings: GET needs features:read
	// (every map user fetches the list to build its picker) while the mutating
	// verbs need metadata:write, the admin gate. Managed from the same
	// /admin/map-settings page.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/basemaps", basemap.Routes(db))
	// The admin-managed service-layer catalog (migration 0113) -- external/3rd-party
	// WMS/WMTS/XYZ/vector-tile/Esri MapServer/Esri FeatureServer/WFS overlays the
	// map can stack on top of the basemap. Same method-aware gate as basemaps: GET
	// needs features:read (every map user fetches the list for its layers panel)
	// while the mutating verbs need metadata:write, the admin gate. Managed from the
	// same /admin/map-settings page. credCipher seals per-layer credentials at rest,
	// exactly like external-systems.
	r.With(auth.RequirePermission(auth.PermFeaturesRead, auth.PermMetadataWrite)).Mount("/service-layers", servicelayers.Routes(db, credCipher))
	// Phase 32 Slice 2 (docs/phase-32-external-links.md): the external-systems
	// catalog a feature's kind='api' links fetch from. Admin config like domains
	// and material specs, so it rides the same metadata:read/write gate; its page
	// lives under /admin/external-systems (frontend) while the API sits top-level,
	// same split as field-packs. credCipher seals credentials at rest. Both read
	// and write require metadata:write so the whole surface is admin-only, matching
	// the /admin catalog: non-admins get a clean 403 rather than a viewable list.
	r.With(auth.RequirePermission(auth.PermMetadataWrite, auth.PermMetadataWrite)).Mount("/external-systems", externalsystem.Routes(db, credCipher))
	// Inbound webhook management (0124, internal/webhook): the admin surface that
	// configures the ingestion endpoints the public /webhooks receiver above
	// serves, plus each endpoint's delivery log, the paste-and-apply tester, and
	// delivery replay. Admin config like external-systems, so it rides the same
	// metadata:read/write gate; its page lives under /admin/webhooks (frontend)
	// while the API sits top-level, the same split as external-systems.
	// credCipher seals each endpoint's HMAC signing secret at rest. Both read and
	// write require metadata:write so the whole surface is admin-only, matching the
	// /admin catalog: non-admins get a clean 403 rather than a viewable list.
	r.With(auth.RequirePermission(auth.PermMetadataWrite, auth.PermMetadataWrite)).Mount("/webhook-endpoints", webhook.AdminRoutes(db, credCipher))
	// Phase 21 pack management (docs/phase-21-field-tool.md): an admin lists
	// every user's field-tool region packs and disables (hides from the device)
	// or deletes them, so stale packs don't crowd a field tech's tablet. This is
	// the web/session counterpart to cmd/fieldsync's bearer-authed, owner-scoped
	// device surface -- a single admin:field-packs gate covers every method (the
	// GET list and the disable/enable/delete actions are one administrative
	// capability), and it reuses the exportStore to reap a deleted pack's RustFS
	// artifacts.
	r.With(auth.RequirePermission(auth.PermFieldPacksAdmin, auth.PermFieldPacksAdmin)).Mount("/field-packs", fieldpack.AdminRoutes(db, exportStore))
	// Phase 23 Slice 3 (docs/phase-23-sync-inbox.md): the field-sync review queue --
	// the QA/system-of-record face of ops.sync_inbox. When a feature class is set to
	// 'review' mode, its device ops land pending_review and a supervisor posts (or
	// rejects) them here before they reach production. A method-aware gate: GET (view
	// the queue and the policy) needs sync:review; the post/reject/policy-set
	// mutations need sync:post.
	r.With(auth.RequirePermission(auth.PermSyncReview, auth.PermSyncPost)).Mount("/sync-review", fieldpack.AdminReviewRoutes(db))
	r.Mount("/auth", auth.Routes(db, authCfg))
	// Field-app distribution -- PUBLIC (no auth): a fresh phone downloads the
	// signed APK with no web login. Only mounted when the APK store configured.
	if appStore != nil {
		r.Mount("/app", appdist.Routes(appStore))
	}

	// Phase 17: export jobs are drained by the standalone cmd/exportworker
	// process (internal/export/worker.go), not the API -- so a large export
	// never competes with request handling. The API only enqueues jobs and mints
	// download URLs; run `task exportworker-dev` (or the compose service)
	// alongside this server, or queued exports will sit unprocessed.

	log.Printf("voltedge api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

// healthzHandler proves the api container can actually reach Postgres, not
// just that the process is running -- a plain 200 with no DB check would
// hide a broken DATABASE_URL until the first real request fails.
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
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	}
}

// featureClassResponse is deliberately its own type, not
// metadata.FeatureClass serialized directly -- that type has no json
// tags (nothing before this needed it over the wire) and carries a
// couple of internal-only fields (MetadataVersionID) with no reason to
// ever leave the API.
type featureClassResponse struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	GeometryType string `json:"geometry_type"`
	NetworkRole  string `json:"network_role"`
	// FunctionalRole is the class's electrical function (migration 0091), nil for
	// an unclassified / non-electrical class -- orthogonal to network_role.
	FunctionalRole *string `json:"functional_role,omitempty"`
	// BoundaryRole is the class's network-sectioning function (migration 0129):
	// source/protective/switching/transformer, or nil for a non-boundary class.
	// The frontend derives each feature's "section" by climbing its trace/load-flow
	// parent tree to the nearest boundary-role ancestor -- no engine change needed.
	BoundaryRole        *string `json:"boundary_role,omitempty"`
	IsDiagramContainer  bool    `json:"is_diagram_container"`
	IsDiagramPlaceable  bool    `json:"is_diagram_placeable"`
	GeometryOptional    bool    `json:"geometry_optional"`
	DiagramGeometryType *string `json:"diagram_geometry_type,omitempty"`
	// Category is the admin-defined "New feature" menu grouping (migration
	// 0074); nil for an uncategorised class (the menu buckets it under
	// "Other").
	Category *string `json:"category,omitempty"`
}

// featureClassesHandler is what lets the frontend show a real,
// admin-set display name (e.g. "MV Conductor") instead of the bare
// table/tile-source id (e.g. "mv_conductor") Martin's catalog only
// knows -- see docs/phase-3-map-loop.md's follow-up on layer naming.
func featureClassesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		classes, err := metadata.ListFeatureClasses(r.Context(), db)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]featureClassResponse, len(classes))
		for i, fc := range classes {
			out[i] = featureClassResponse{
				Code:                fc.Code,
				Name:                fc.Name,
				GeometryType:        fc.GeometryType,
				NetworkRole:         fc.NetworkRole,
				FunctionalRole:      fc.FunctionalRole,
				BoundaryRole:        fc.BoundaryRole,
				IsDiagramContainer:  fc.IsDiagramContainer,
				IsDiagramPlaceable:  fc.IsDiagramPlaceable,
				GeometryOptional:    fc.GeometryOptional,
				DiagramGeometryType: fc.DiagramGeometryType,
				Category:            fc.Category,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"feature_classes": out})
	}
}

// symbologyIcon is one symbology_icons row over the wire -- the SVG
// markup behind an icon_ref a symbology rule references. Its own type
// rather than metadata.SymbologyIcon serialized directly, matching how
// this file keeps wire-shaped copies of everything else it returns.
type symbologyIcon struct {
	IconRef string `json:"icon_ref"`
	SVG     string `json:"svg"`
}

// diagramStencil is one symbol stencil palette entry over the wire (Phase 24
// Slice 2). Its glyph SVG is NOT included here -- it's the shared
// symbology_icons asset the frontend already loads via GET /symbology/icons
// and resolves by icon_ref, so the palette reuses that registry rather than
// re-shipping markup. This carries only the catalog: which icon, and how the
// picker labels and groups it.
type diagramStencil struct {
	IconRef   string `json:"icon_ref"`
	Label     string `json:"label"`
	Category  string `json:"category"`
	SortOrder int    `json:"sort_order"`
	// PromoteClass, when set, is the feature class code a stamped symbol of this
	// stencil can be promoted into (Phase 25) -- the frontend offers "promote to
	// a <class> feature" on double-click. Omitted for decoration-only stencils.
	PromoteClass *string `json:"promote_class,omitempty"`
}

// diagramStencilsHandler serves the whole stencil palette at once (GET
// /symbology/stencils), the same small-global-set / resolve-client-side shape
// symbologyIconsHandler uses. Gated by features:read alongside it -- any
// viewer who can load the map can see the palette.
func diagramStencilsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stencils, err := metadata.ListDiagramStencils(r.Context(), db)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]diagramStencil, len(stencils))
		for i, s := range stencils {
			out[i] = diagramStencil{IconRef: s.IconRef, Label: s.Label, Category: s.Category, SortOrder: s.SortOrder, PromoteClass: s.PromoteClass}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"stencils": out})
	}
}

// symbologyIconsHandler serves the whole icon set at once -- the frontend
// (web/src/hooks/use-symbology-icons.ts) fetches it once and resolves
// every rule's icon_ref against it client-side, the same way it already
// resolves symbology_rules. A small, global set, so no per-class scoping
// (unlike the per-feature-class schema endpoint); Martin never sees it.
func symbologyIconsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		icons, err := metadata.ListSymbologyIcons(r.Context(), db)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		out := make([]symbologyIcon, len(icons))
		for i, ic := range icons {
			out[i] = symbologyIcon{IconRef: ic.IconRef, SVG: ic.SVG}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"icons": out})
	}
}

// subtypeSchema is one row of a feature class's subtypes, code + name
// only -- everything a form needs to offer a subtype picker.
type subtypeSchema struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}

// fieldSchema is everything a client needs to render one correct form
// control for one field, and to know when it's required, without
// re-deriving api/internal/crud's validation rules independently.
// RequiredForSubtypes is flattened from metadata.subtype_field_rules
// here rather than shipped as raw rule rows, since the only thing a
// form actually needs from that table is "is this required for the
// subtype the user picked" -- a field with a default_override for a
// given subtype is deliberately left out of that subtype's list, same
// as api/internal/crud/validate.go's validateRequired: the rule is
// satisfied by the fallback value, not by demanding the client send one.
type fieldSchema struct {
	Name                 string   `json:"name"`
	DataType             string   `json:"data_type"`
	Nullable             bool     `json:"nullable"`
	VisibleOnCreate      bool     `json:"visible_on_create"`
	VisibleOnUpdate      bool     `json:"visible_on_update"`
	EditableAfterPublish bool     `json:"editable_after_publish"`
	DomainCode           *string  `json:"domain_code,omitempty"`
	DomainValues         []string `json:"domain_values,omitempty"`
	// DomainValueLabels maps each coded value to its human label (e.g.
	// "CLASS_1" -> "Class 1 (9 m)"), so a read-only panel and the edit
	// dropdown can show the admin-set label instead of the bare stored code.
	// Keyed by code; the code stays the source of truth (what's stored and
	// validated), this is display sugar only.
	DomainValueLabels    map[string]string `json:"domain_value_labels,omitempty"`
	RangeMin             *float64 `json:"range_min,omitempty"`
	RangeMax             *float64 `json:"range_max,omitempty"`
	RequiredForSubtypes  []int    `json:"required_for_subtypes,omitempty"`
	// DefaultValue is the field-level default (metadata.fields.default_value,
	// migration 0150), as text (a coded/domain field holds the domain value
	// code). The create form pre-fills it; omitted when unset. Per-subtype
	// overrides (subtype_field_rules.default_override) aren't surfaced here yet.
	DefaultValue *string `json:"default_value,omitempty"`
	// MaterialSpecs is keyed by domain value code, only present for the
	// subset of DomainValues that carry a material_spec_id
	// (metadata.domain_values, migrations/0021) -- lets a consumer go
	// straight from "this field's current value is ACSR_50" to its real
	// engineering numbers (the map feature panel's own Materials tab)
	// without a second, admin-only round trip.
	MaterialSpecs map[string]materialSpecSummary `json:"material_specs,omitempty"`
}

// materialSpecSummary mirrors admin's own materialSpecResponse --
// duplicated rather than imported, the same way this package already
// keeps its own wire-shaped copies of everything else it returns
// (compileResponse mirrors compiler.Result for the identical reason,
// see api/internal/admin/types.go).
type materialSpecSummary struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	ResistanceOhmPerKm *float64 `json:"resistance_ohm_per_km,omitempty"`
	ReactanceOhmPerKm  *float64 `json:"reactance_ohm_per_km,omitempty"`
	AmpacityAmps       *float64 `json:"ampacity_amps,omitempty"`
	DiameterMm         *float64 `json:"diameter_mm,omitempty"`
	WeightKgPerKm      *float64 `json:"weight_kg_per_km,omitempty"`
}

// terminalSchema is one named connection point a junction feature class
// defines (e.g. DIST_XFMR's T1/T2/T3) -- everything a connectivity UI
// needs to offer a terminal picker without re-deriving
// metadata.Loaded.ResolveTerminal's subtype-scoping itself. SubtypeCode
// nil means the terminal applies to every subtype of the feature class
// (metadata.Terminal.SubtypeID nil, same meaning); a non-nil value
// scopes it to one specific subtype, and per ResolveTerminal's own
// most-specific-wins rule, a subtype-scoped terminal with the same code
// as a generic one takes precedence for that subtype.
type terminalSchema struct {
	Code         string `json:"code"`
	Role         string `json:"role"`
	PhaseCapable bool   `json:"phase_capable"`
	SubtypeCode  *int   `json:"subtype_code,omitempty"`
}

// symbologySchema is one symbology_rules row, pre-resolved for the
// frontend's own most-specific-wins matcher (web/src/lib/symbology.ts)
// -- SubtypeCode/DomainField/DomainValueCode nil means that axis is
// unconstrained (matches any subtype / any attribute value). DomainField
// is resolved server-side rather than making the frontend cross-reference
// domains itself: a rule only ever stores a domain_value_id, and "which
// field is implied by which domain_value" requires matching that value's
// domain against whichever of the feature class's fields uses it (see
// migrations/0012's own column comment) -- work this handler is already
// positioned to do once, not work worth repeating in every consumer.
// RenderContext ("map" or "diagram") is included, not filtered server-side
// -- both the map layer styling and the diagram canvas resolve from this
// same array client-side, each ignoring the rules meant for the other
// context, matching docs/metadata-core.md's "every icon decision happens
// client-side" design.
type symbologySchema struct {
	RenderContext   string          `json:"render_context"`
	SubtypeCode     *int            `json:"subtype_code,omitempty"`
	DomainField     *string         `json:"domain_field,omitempty"`
	DomainValueCode *string         `json:"domain_value_code,omitempty"`
	StyleDef        json.RawMessage `json:"style_def"`
	// MinZoom/MaxZoom were already real columns loaded into
	// metadata.SymbologyRule (see models.go) but never left this
	// package until now -- resolveSymbology (web/src/lib/symbology.ts)
	// couldn't zoom-gate a rule it never received.
	MinZoom *int `json:"min_zoom,omitempty"`
	MaxZoom *int `json:"max_zoom,omitempty"`
}

// featureClassSchemaResponse is the shape a create/edit form -- or a
// connectivity picker -- needs: whether (and what type of) geometry to
// draw, what subtype to pick (if any), what fields to render for it,
// and what terminals (if any) it can be connected through. Deliberately
// separate from featureClassResponse above -- that one backs the layers
// panel and map (cheap, always-fetched summary data); this one is
// heavier (every field + domain values + terminals) and only fetched
// when a form or picker is actually about to be shown.
type featureClassSchemaResponse struct {
	Code         string            `json:"code"`
	Name         string            `json:"name"`
	GeometryType string            `json:"geometry_type"`
	Subtypes     []subtypeSchema   `json:"subtypes"`
	Fields       []fieldSchema     `json:"fields"`
	Terminals    []terminalSchema  `json:"terminals,omitempty"`
	Symbology    []symbologySchema `json:"symbology,omitempty"`
	// Phase 30 per-class map label config, raw jsonb passthrough (like
	// symbologySchema.StyleDef). Omitted when the class has no label_def.
	LabelDef json.RawMessage `json:"label_def,omitempty"`
	// Phase 37 Slice 2 nominal voltage namespace ("MV"/"LV"), omitted when
	// the class isn't network-classified. The create/edit form reads it to
	// scope the circuit_id field's picker to same-network circuits.
	Network *string `json:"network,omitempty"`
	// Map display-zoom band (migration 0090) -- the map hides the whole
	// feature below min_display_zoom / above max_display_zoom. Each omitted
	// when unset (no bound). The map's style function (createSymbologyStyle)
	// reads these; unrelated to a symbology rule's own min_zoom/max_zoom.
	MinDisplayZoom *int `json:"min_display_zoom,omitempty"`
	MaxDisplayZoom *int `json:"max_display_zoom,omitempty"`
	// AnalysisStudies is the effective set of analysis-study ids this class
	// offers (api/internal/studies): applicable by role, minus the admin prune.
	// The feature panel's analysis menu shows only studies whose id is here.
	AnalysisStudies []string `json:"analysis_studies,omitempty"`
}

// resolveSymbologySchema loads every one of a feature class's symbology
// rules (map and diagram context alike) and resolves each one's
// domain_value_id into a (field name, value code) pair the frontend's
// matcher can compare directly against a rendered feature's own
// attributes -- see symbologySchema's own comment for why that
// resolution belongs here, not in the frontend.
func resolveSymbologySchema(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, subtypeCodeByID map[string]int) ([]symbologySchema, error) {
	rules, err := metadata.LoadSymbologyRules(ctx, db, loaded.FeatureClass.ID)
	if err != nil {
		return nil, err
	}

	valueIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.DomainValueID != nil {
			valueIDs = append(valueIDs, *r.DomainValueID)
		}
	}
	var values []metadata.DomainValue
	if len(valueIDs) > 0 {
		if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
			return nil, err
		}
	}
	valueByID := make(map[string]metadata.DomainValue, len(values))
	for _, v := range values {
		valueByID[v.ID] = v
	}

	fieldNameByDomainID := make(map[string]string, len(loaded.Fields))
	for _, f := range loaded.Fields {
		if f.DomainID != nil {
			fieldNameByDomainID[*f.DomainID] = f.Name
		}
	}

	out := make([]symbologySchema, 0, len(rules))
	for _, r := range rules {
		s := symbologySchema{StyleDef: r.StyleDef, RenderContext: r.RenderContext, MinZoom: r.MinZoom, MaxZoom: r.MaxZoom}
		if r.SubtypeID != nil {
			if code, ok := subtypeCodeByID[*r.SubtypeID]; ok {
				s.SubtypeCode = &code
			}
		}
		if r.DomainValueID != nil {
			if v, ok := valueByID[*r.DomainValueID]; ok {
				if fieldName, ok := fieldNameByDomainID[v.DomainID]; ok {
					s.DomainField = &fieldName
				}
				code := v.Code
				s.DomainValueCode = &code
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// resolveMaterialSpecs looks up every domain_values row under domainID
// that carries a material_spec_id, and resolves each into a full
// materialSpecSummary keyed by the domain value's own code. Returns nil
// (not an empty map) when nothing under this domain is material-linked,
// so fieldSchema.MaterialSpecs' own omitempty leaves it out of the
// response entirely for the common case (most domains carry no
// material data at all).
func resolveMaterialSpecs(ctx context.Context, db *bun.DB, domainID string) (map[string]materialSpecSummary, error) {
	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Where("domain_id = ? AND material_spec_id IS NOT NULL", domainID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading material-linked domain values: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}

	specIDs := make([]string, len(values))
	for i, v := range values {
		specIDs[i] = *v.MaterialSpecID
	}
	var specs []metadata.MaterialSpec
	if err := db.NewSelect().Model(&specs).Where("id IN (?)", bun.In(specIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading material specs: %w", err)
	}
	specByID := make(map[string]metadata.MaterialSpec, len(specs))
	for _, s := range specs {
		specByID[s.ID] = s
	}

	out := make(map[string]materialSpecSummary, len(values))
	for _, v := range values {
		s, ok := specByID[*v.MaterialSpecID]
		if !ok {
			continue
		}
		out[v.Code] = materialSpecSummary{
			Code: s.Code, Name: s.Name, Category: s.Category,
			ResistanceOhmPerKm: s.ResistanceOhmPerKm, ReactanceOhmPerKm: s.ReactanceOhmPerKm,
			AmpacityAmps: s.AmpacityAmps, DiameterMm: s.DiameterMm, WeightKgPerKm: s.WeightKgPerKm,
		}
	}
	return out, nil
}

// resolveDomainLabels returns each coded value's human label keyed by its own
// code (e.g. "CLASS_1" -> "Class 1 (9 m)"), so a client can show the admin-set
// label instead of the raw stored code. Returns nil when the domain has no
// values, so fieldSchema.DomainValueLabels' omitempty drops it from the response.
func resolveDomainLabels(ctx context.Context, db *bun.DB, domainID string) (map[string]string, error) {
	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Column("code", "label").Where("domain_id = ?", domainID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading domain value labels: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, v := range values {
		out[v.Code] = v.Label
	}
	return out, nil
}

func featureClassSchemaHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")

		loaded, err := metadata.Load(r.Context(), db, code)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		subtypes := make([]subtypeSchema, len(loaded.Subtypes))
		for i, s := range loaded.Subtypes {
			subtypes[i] = subtypeSchema{Code: s.Code, Name: s.Name}
		}

		fields := make([]fieldSchema, len(loaded.Fields))
		for i, f := range loaded.Fields {
			fs := fieldSchema{
				Name:                 f.Name,
				DataType:             f.DataType,
				Nullable:             f.Nullable,
				VisibleOnCreate:      f.VisibleOnCreate,
				VisibleOnUpdate:      f.VisibleOnUpdate,
				EditableAfterPublish: f.EditableAfterPublish,
				DefaultValue:         f.DefaultValue,
			}

			if f.DomainID != nil {
				if d, ok := loaded.Domains[*f.DomainID]; ok {
					code := d.Code
					fs.DomainCode = &code
					if d.DomainType == "coded" {
						fs.DomainValues = loaded.DomainValues[d.ID]
						labels, err := resolveDomainLabels(r.Context(), db, d.ID)
						if err != nil {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusInternalServerError)
							json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
							return
						}
						fs.DomainValueLabels = labels
						materialSpecs, err := resolveMaterialSpecs(r.Context(), db, d.ID)
						if err != nil {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusInternalServerError)
							json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
							return
						}
						fs.MaterialSpecs = materialSpecs
					} else if d.DomainType == "range" {
						fs.RangeMin = d.RangeMin
						fs.RangeMax = d.RangeMax
					}
				}
			}

			for _, st := range loaded.Subtypes {
				for _, rule := range loaded.SubtypeFieldRules {
					if rule.SubtypeID == st.ID && rule.FieldID == f.ID && rule.IsRequired && rule.DefaultOverride == nil {
						fs.RequiredForSubtypes = append(fs.RequiredForSubtypes, st.Code)
					}
				}
			}

			fields[i] = fs
		}

		subtypeCodeByID := make(map[string]int, len(loaded.Subtypes))
		for _, st := range loaded.Subtypes {
			subtypeCodeByID[st.ID] = st.Code
		}

		terminals := make([]terminalSchema, len(loaded.Terminals))
		for i, t := range loaded.Terminals {
			ts := terminalSchema{Code: t.Code, Role: t.Role, PhaseCapable: t.PhaseCapable}
			if t.SubtypeID != nil {
				if code, ok := subtypeCodeByID[*t.SubtypeID]; ok {
					ts.SubtypeCode = &code
				}
			}
			terminals[i] = ts
		}

		symbology, err := resolveSymbologySchema(r.Context(), db, loaded, subtypeCodeByID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Effective analysis studies for this class (applicable by role, minus
		// the admin prune) -- the feature panel reads this to scope its menu.
		disabledStudies, err := studies.LoadDisabled(r.Context(), db, loaded.FeatureClass.ID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(featureClassSchemaResponse{
			Code:            loaded.FeatureClass.Code,
			Name:            loaded.FeatureClass.Name,
			GeometryType:    loaded.FeatureClass.GeometryType,
			Subtypes:        subtypes,
			Fields:          fields,
			Terminals:       terminals,
			Symbology:       symbology,
			LabelDef:        loaded.FeatureClass.LabelDef,
			Network:         loaded.FeatureClass.Network,
			MinDisplayZoom:  loaded.FeatureClass.MinDisplayZoom,
			MaxDisplayZoom:  loaded.FeatureClass.MaxDisplayZoom,
			AnalysisStudies: studies.Effective(loaded.FeatureClass, disabledStudies),
		})
	}
}
