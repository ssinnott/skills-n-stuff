#!/usr/bin/env bash
# Build a deterministic git repo with a messy feature-branch history, for the
# reorg action eval. Usage: make-messy-repo.sh <dest-dir>
# Tags: `base` (main tip the branch grew from), `original-tip` (feature tip
# before any reorg — the tree-identity anchor the checker compares against).
set -euo pipefail
dest=${1:?usage: make-messy-repo.sh <dest-dir>}
export GIT_AUTHOR_NAME=dev GIT_AUTHOR_EMAIL=dev@example.com
export GIT_COMMITTER_NAME=dev GIT_COMMITTER_EMAIL=dev@example.com

git init -q -b main "$dest"
cd "$dest"

echo "# orderflow" > README.md
git add -A && git commit -qm "Initial commit"
git tag base
git checkout -qb feature

mkdir -p src test

cat > src/store.js <<'EOF'
// idempotency store (half done)
export const store = {};
EOF
git add -A && git commit -qm "wip"

cat > src/store.js <<'EOF'
// redis-backed idempotency stroe
export const store = {
  async get(key) { return redis.get(`idem:${key}`); },
  async put(key, value) { return redis.set(`idem:${key}`, value); },
};
EOF
git add -A && git commit -qm "idempotency store"

sed -i 's/stroe/store/' src/store.js
git add -A && git commit -qm "fix"

cat > src/submit.js <<'EOF'
import { store } from "./store.js";
export async function submitOrder(order, key) {
  const existing = await store.get(key);
  if (existing) return { result: existing, replayed: true };
  const receipt = await charge(order);
  await enqueue(order, key);
  await store.put(key, receipt);
  return { result: receipt, replayed: false };
}
EOF
cat > src/worker.js <<'EOF'
import { store } from "./store.js";
// procesing fulfillment jobs
export async function fulfill(job) {
  await ship(job.orderId);
}
EOF
git add -A && git commit -qm "thread key thru queue"

sed -i 's/procesing/processing/' src/worker.js
git add -A && git commit -qm "oops typo"

cat > test/store.test.js <<'EOF'
test("replays stored receipt", async () => {
  await store.put("k1", { ok: true });
  expect(await store.get("k1")).toEqual({ ok: true });
});
EOF
git add -A && git commit -qm "tests"

cat > src/worker.js <<'EOF'
import { store } from "./store.js";
// processing fulfillment jobs
export async function fulfill(job) {
  const first = await store.put(`claim:${job.key}`, 1);
  if (!first) return;
  await ship(job.orderId);
}
EOF
cat > test/worker.test.js <<'EOF'
test("ships only once across redeliveries", async () => {
  await fulfill(job); await fulfill(job);
  expect(shipCalls).toBe(1);
});
EOF
git add -A && git commit -qm "more tests + fix worker claim again"

git tag original-tip
echo "messy repo ready at $dest (branch: feature, tags: base, original-tip)"
