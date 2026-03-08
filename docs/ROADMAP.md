# EntityStore Roadmap

## Overview

Consolidate entitystore as the canonical home for entity storage, matching, and the protoc plugin that lets downstream projects define their own entity types via proto annotations. Bring server infrastructure to parity with objectstore.

## Naming

- `protoc-gen-llm-extract` → **`protoc-gen-entitystore`**
- `llm_extract/v1/options.proto` → **`entitystore/v1/options.proto`** (same proto package as the service)
- Import path: `github.com/laenen-partners/entitystore/matching` (already correct)

## Phases

### Phase 1 — Security & Server Infrastructure
Bring entitystore to objectstore parity.

- [x] `auth.go` — API key auth interceptor (Bearer, constant-time compare, caller propagation)
- [x] `caller.go` — Caller context (X-User-ID, X-Service-ID)
- [x] `middleware.go` — Rate limiting, CORS, security headers, request logging
- [x] `config.go` — Add Addr, APIKeys, RateLimit, RateBurst, CORSOrigins
- [x] `server.go` — Wire auth, middleware stack, health endpoints
- [x] `cmd/estore/main.go` — Graceful shutdown (signal.NotifyContext, 30s drain)

### Phase 2 — Protoc Plugin Migration
Move protoc-gen-llm-extract into entitystore and rename.

- [x] Move `cmd/protoc-gen-llm-extract/main.go` → `cmd/protoc-gen-entitystore/main.go`
- [x] Merge `llm_extract/v1/options.proto` into `entitystore/v1/options.proto`
- [x] Update generated code references (import paths, package names)
- [x] Update `buf.gen.yaml` to include the plugin
- [x] Add `Taskfile.yml` target for plugin install (`generate:plugin`)
- [x] Verify generation works end-to-end

### Phase 3 — Build & CI
- [x] `mise.toml` — Pin Go, buf, sqlc, task versions
- [x] `.env.sample` — Document all env vars
- [x] `Dockerfile` — Multi-stage distroless build
- [x] `.github/workflows/ci.yml` — mise-action, lint, build, vet, test:cover

### Phase 4 — API Completeness
- [ ] Add `ResolveEntity` RPC — Expose `ApplyMatchDecision` for the extraction pipeline
- [ ] Add `MergeEntity` RPC — Expose JSONB merge for partial updates
- [ ] Consider batch RPCs for bulk extraction workflows

## Downstream Impact

Once complete, downstream projects (e.g. jobs) will:
1. Import `entitystore/v1/options.proto` as a buf dependency
2. Annotate their proto messages with `(entitystore.v1.field)` and `(entitystore.v1.message)`
3. Add `protoc-gen-entitystore` to their `buf.gen.yaml`
4. Run `buf generate` → get `{Message}MatchConfig()` functions
5. Register configs with `matching.NewMatchConfigRegistry()`
6. Call entitystore RPCs to resolve, query, and relate entities
