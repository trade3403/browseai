"""Race real PostgreSQL connections for one global repository/branch lease."""
import concurrent.futures
import subprocess
import threading

barrier = threading.Barrier(2)

def contender(n):
    tenant = f"00000000-0000-4000-8000-00000000000{n}"
    project = f"00000000-0000-4000-8000-00000000001{n}"
    task = f"00000000-0000-4000-8000-00000000002{n}"
    sql = f"""
BEGIN;
SET LOCAL ROLE browseai_app;
SET LOCAL lock_timeout = '15s';
SET LOCAL browseai.tenant_id = '{tenant}';
INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,project_id,task_id,expires_at)
VALUES (123,'feat/demo','{tenant}','{project}','{task}',now()+interval '1 minute')
ON CONFLICT (repository_id,code_branch) DO NOTHING
RETURNING fence;
SELECT pg_sleep(0.3);
COMMIT;
"""
    barrier.wait(timeout=10)
    result = subprocess.run(["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql],
                            capture_output=True, text=True, check=True, timeout=25)
    return n, [int(line) for line in result.stdout.splitlines() if line.isdigit()]

with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    results = list(pool.map(contender, (1, 2)))
winners = [(n, fences[0]) for n, fences in results if len(fences) == 1]
assert len(winners) == 1 and sum(len(fences) for _, fences in results) == 1, results
winner, first_fence = winners[0]

def query(sql):
    return subprocess.check_output(["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql],
                                   text=True, timeout=15).strip()

assert query("SELECT count(*) FROM browseai.leases") == "1"
loser = 3 - winner
tenant = f"00000000-0000-4000-8000-00000000000{loser}"
project = f"00000000-0000-4000-8000-00000000001{loser}"
task = f"00000000-0000-4000-8000-00000000002{loser}"
assert query(f"BEGIN; SET LOCAL ROLE browseai_app; SET LOCAL browseai.tenant_id = '{tenant}'; SELECT count(*) FROM browseai.leases; COMMIT;") == "0"
assert query(f"BEGIN; SET LOCAL ROLE browseai_app; SET LOCAL browseai.tenant_id = '{tenant}'; DELETE FROM browseai.leases WHERE repository_id=123 RETURNING fence; COMMIT;") == ""
# Trusted test owner simulates reconciliation and release, not automatic expiry.
query("DELETE FROM browseai.leases WHERE repository_id=123")
second_fence = int(query(f"""BEGIN; SET LOCAL ROLE browseai_app; SET LOCAL browseai.tenant_id = '{tenant}';
INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,project_id,task_id,expires_at)
VALUES (123,'feat/demo','{tenant}','{project}','{task}',now()+interval '1 minute') RETURNING fence;
COMMIT;"""))
assert second_fence > first_fence
print("Global lease race: one winner; other tenant cannot see/delete lease; fencing increases after release")
