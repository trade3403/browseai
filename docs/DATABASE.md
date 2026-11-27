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
