# V0.3 GitHub integration — development preview

## Scope
The first batch implements a GitHub App JWT signer, a narrowly scoped installation-token client, and the local authorization boundary. It does not register an app, use real credentials, expose a binding endpoint or perform automated dispatch. All contract tests use synthetic RSA keys and an in-memory HTTP transport.

## Permissions and secrets
Use a GitHub App with read-only Actions, Checks, Contents, Pull requests and mandatory Metadata. Do not grant write/admin permissions for this monitoring adapter. JWT RS256, iat backdated 60 seconds, exp five minutes in the future. The request to the fixed api.github.com installation token endpoint lists exactly one repository ID and explicitly requests read-only permissions. The response is rejected if permissions/repo/expiry differ from the expected scope; token format is opaque and never logged or persisted. The client refuses redirects and bounds time and response size. API version pinned to 2026-03-10. Reference: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app and https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app.

Private keys must be supplied through a protected operator secret mechanism in a future batch; never commit them. No real app credentials are needed for CI.

## Tenant boundary
Migration 0008 stores only installation ID, tenant ID, enabled flag and repository IDs/names, no secret. The runtime application has SELECT only with forced RLS; mapping is provisioned by a trusted operator only after independent verification, not from a user-supplied installation ID. Each installation belongs to one workspace. Before minting, resolve the authenticated user's project to an enabled mapping with matching stable repository ID and name. Actual installation ownership, allowed repository/branch revalidation, authenticated setup flow and revocation reconciliation are pending; don't treat a locally inserted mapping as independent proof of GitHub ownership. No real integration is wired into HTTP startup.

## Remaining V0.3
1. Trusted installation verification/configuration and read-only API transport with token refresh, bounded retry and rate-limit handling.
2. Authenticated webhook HMAC, delivery ledger, installation-to-tenant routing and replay/ordering safeguards.
3. Polling with pagination and exact repository, code SHA, branch/PR merge SHA and workflow/job attempt evidence.
4. Integration with persistent state machine, negative evidence and end-to-end mock contract tests; phase acceptance.

## Read-only branch observation
The internal observer derives the immutable project branch and enabled installation/repository binding from the authenticated tenant. It requests a fresh scoped token, reads the fixed GitHub repository and branch endpoints, validates repository stable ID/name, rejects disabled/archived repositories, checks exact branch name and lowercase 40-digit head SHA, then rechecks the local mapping. Only a successful observation yields a verifiedBranch and head SHA. This is an observation, not a lock or proof that the branch cannot subsequently change. No HTTP endpoint exposes tokens. GET requests refuse redirects, have a bounded response and return generic errors. Explicit rate-limit responses and 5xx return a retry deadline; no inline retry or polling worker is enabled. Installation-token rate-limit handling, pagination, independent installation ownership proof, secure operator setup and runtime wiring remain to be implemented. All new tests use mock transport; no live installation has been configured.

## Batch 3a: operator verification primitives
Protected absolute-path PEM loading (PKCS#1 or PKCS#8 RSA) and App JWT identity, installation account/target, suspension and scoped repository validation are implemented as internal primitives. Use a trusted directory and restrictive POSIX file permissions; Windows ACL support and the actual privileged provisioning command are pending. Operator approval must be independently obtained, not inferred from App access. No HTTP self-binding is exposed. No live credentials configured. CI pending.
