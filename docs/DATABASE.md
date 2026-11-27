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

## Internal branch leases
leaseStore acquires one global stable repository ID + exact code branch lease for a queued task at the expected revision. The trusted caller must first verify GitHub authorization and identity (adapter pending V0.3). Lease duration is 60 seconds using database time, renewal is same-fence only, expired rows are not automatically replaced. Release requires inactive task state plus trusted reconciliation; stopping or pausing alone does not establish external writer termination. The ready, prepare, confirm_sent and repair persistence paths check the current live fence inside the task transaction. No HTTP lease routes, external write fencing, automatic recovery or browser dispatch are implemented. Tests use synthetic repo IDs and real isolated PostgreSQL connections.

## Mutation idempotency, audit and durable events (0004)
All project/task POST/PATCH/DELETE routes now require one Idempotency-Key header (1–128 ASCII letters, digits, underscore or hyphen). A key is scoped to the authenticated tenant and user, and binds action, resource ID, revision and canonical decoded input. A committed duplicate returns the original result, a different request using the same key returns 409. Failed transactions do not reserve keys. Internal trusted task transitions also append audit/event metadata. Audit and event rows are append-only for the runtime app role and tenant filtered; audit/event records contain IDs, action and revision only. This is a durable event foundation, not a working delivery queue, and does not yet cover account administration or lease renewals. Cached results are limited to project/task records; retention and archival remain future work. The new migration is required for authenticated readiness; run it after 0003 and roll back in reverse order only with approved backups. CI uses synthetic data.

## Account lifecycle (0005)
Provision a separate browseai_admin login with no elevated role membership and no direct table permissions. It may execute only the three account-management functions. The controller binary accepts account provision LOGIN WORKSPACE, account disable LOGIN, account enable LOGIN, and account reset-password LOGIN with BROWSEAI_ADMIN_DSN (verify-full). Provision/reset read one password line from stdin; use a protected non-echoing input or secret pipe, never a password in CLI arguments, shell history or source code. No public registration or default password. Account changes append a separate account_audit row identified by the authenticated DB operator. Disabling/re-enabling a user or tenant and changing a password revoke all affected existing sessions permanently. Real login uses a locked password-proof recheck at issuance, avoiding stale password verification after concurrent reset. The offline operator is not a web platform-admin UI; browser/node control revocation and GitHub revocation require the later adapters. The 0005 rollback removes its audit table and safeguards: only use disposable CI or explicitly approved backup-backed rollback.

## Safe local resume and approval intent (0006)
POST /api/tasks/{id}/resume and /approve accept only revision and the required Idempotency-Key header. Public resume is restricted to a paused, never-dispatched first-stage queued draft without a lease or dispatch-related audit history. Other resumes need internal trusted reconciliation. Approval is an append-only tenant-scoped intent tied to revision, stage, live fence and actor; it does not change state or send instructions. The internal confirm_sent transition requires the matching approval plus independently trusted manual-send evidence. A revision change invalidates an old intent. V0.5 must additionally bind the exact immutable dispatch instruction and real manual confirmation; this is not a browser dispatch system. GitHub and external recovery remain later-phase work.
