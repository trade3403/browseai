# V0.2 PostgreSQL foundation

Migration 0001 creates the browseai schema, tenant/user/membership/session tables,
projects/tasks/stages and global repository-ID + branch leases. Application role
is provisioned separately: NOSUPERUSER, NOBYPASSRLS, not a schema/table owner and
not a member of an elevated role. CI uses a NOLOGIN role through SET LOCAL ROLE
on an isolated PostgreSQL 16 service; it is not a production login configuration.

The trusted application must derive tenant identity from authenticated server
state and set browseai.tenant_id with a transaction-local parameterized
set_config call. RLS is not a defense against arbitrary SQL execution by the
application role, nor a substitute for service authorization. Do not grant
database access to end users. The task role has no direct identity/session table
privileges. Authentication role, password hashing, session revocation, API
persistence and transactional state-machine/lease operations are not wired yet.

Migrations are run by a separate owner. The initial down migration drops the
schema and data: only for disposable CI or approved backup-backed rollback.
Never run it as an automatic production startup action. PostgreSQL 16 is the
chosen development baseline; runtime Compose remains the explicit V0.1 mock
until authenticated persistence is connected.

The global lease primary key prevents cross-tenant simultaneous rows. Fencing
tokens increase via a global sequence; an expiry timestamp alone cannot grant
ownership. Actual renew/release/fencing enforcement and reconciliation must be
implemented in the trusted service; raw SQL tests establish constraints only.
Generic conflict responses must hide the holder's identity.

CI applies up, seeds two synthetic tenants, tests default-deny/read/write/FK
isolation, revision comparison, identity-table denial, concurrent global lease
insert, fencing progression, then down and up again. No real accounts or secrets.

## 0002 session authentication foundation
Provision separate browseai_auth role (NOSUPERUSER NOBYPASSRLS, no elevated membership). Apply 0002 after 0001, roll back in reverse order. The auth pool may execute only issue_session, resolve_session and revoke_session, not read credential/project tables directly. SECURITY DEFINER functions are fully schema-qualified, with restricted search path and PUBLIC execution revoked. Use a separate non-superuser migration owner in production. The trusted credential verifier must authenticate before issue_session; the function validates active membership, but does not verify passwords. Tokens are 32 random bytes, URL-safe encoded, stored only as SHA-256 digests, expire in eight hours. Resolve rechecks current membership and enabled flags; revocation blocks subsequent resolution. Account disabling must additionally revoke sessions and leases in the future admin service; currently re-enabling an account could reactivate an otherwise unexpired session. Runtime driver/bootstrap, credential verification and HTTP endpoints remain unimplemented. No real account is used in CI.

## 0003 credential verification and authenticated preview
The auth role additionally executes login_candidate(text), which returns a password hash only to the trusted verifier and only for a unique enabled membership. Hashes use Argon2id v19, 64 MiB, 3 iterations, parallelism 1, 16-byte random salt, 32-byte output. No public registration or default account. Provision via trusted administration; offline hashPassword helper is not yet a CLI. The authentication-only controller mode requires BROWSEAI_MODE=authenticated, BROWSEAI_AUTH_DSN (postgres URL with browseai_auth role and sslmode=verify-full), BROWSEAI_ORIGIN (external HTTPS origin), and loopback LISTEN_ADDR behind a trusted TLS proxy. It fails startup if DB role/schema invalid; no mock project endpoints are available. The current Compose still starts the mock, not the authenticated stack. CI uses disposable isolated PostgreSQL with TLS disabled only for test connections, not runtime startup. Real login/session integration is tested with two synthetic tenants. Password rotation/re-enable revocation and account provisioning, full proxy/deployment support, project/task APIs and browser login UI remain pending. Production dependency audit and upgrades remain required.

## Project CRUD preview
Authenticated mode now also requires BROWSEAI_APP_DSN for a separate browseai_app login (sslmode=verify-full). Project endpoints use a server-derived principal and transaction-local tenant setting plus explicit tenant filters. GET /api/projects returns at most 100 projects with next_cursor; GET /api/projects/{id}, POST /api/projects, PATCH /api/projects/{id} (name and revision), DELETE /api/projects/{id}?revision=N. Repository ID/name/branch are configuration only until verified GitHub authorization in V0.3; they are immutable in this preview. Project deletion with tasks is rejected. Admins have no default private project access. Full-suite coverage >=75% is enforced in the mandatory database job with actual PostgreSQL integration, rather than treating SQL functions as untested in the unit-only coverage measure. No real deployment yet.

## Task CRUD and persistent state transitions
GET/POST /api/tasks, GET/PATCH/DELETE /api/tasks/{id}, POST /api/tasks/{id}/pause and /stop are available in authenticated preview. Create accepts project_id, name, stages (id and positive weight), optional max_repairs (default 5), and creates task plus stage rows in one transaction. List supports after and project_id. PATCH changes name only and requires revision; DELETE requires revision and is restricted to pristine QUEUED drafts at revision 0. State changes take row lock and CAS revision, store actual taskSnapshot fields; HTTP only exposes pause/stop with revision and never accepts trusted evidence. Resume/approve remain unavailable until reconciliation and approval adapters exist. Create idempotency, full stage configuration/commands, audit/outbox and transactional leases remain pending. No browser dispatch is enabled.
