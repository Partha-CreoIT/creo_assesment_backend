# Load / capacity / chaos test harness

Capacity certification for the exam platform: prove 300 concurrent candidates
can complete a realistic exam without data corruption, incorrect submissions,
unacceptable error rates, or runaway latency, so a real 200-candidate exam has
comfortable headroom. See `internal/config/config.go`'s `RUN_CONCURRENCY` /
`GRADER_WORKERS` / `RUN_QUEUE_TIMEOUT_SEC` / `RUN_RATE_LIMIT_SEC` — these are
the tunables this harness sweeps to find the real capacity ceiling instead of
guessing at it.

This is a large, phased effort (see "Status" below) — build it out
incrementally rather than expecting every scenario listed here to exist yet.

## Testing a deployed server

Every scenario is driven entirely by env vars — no local infra required to
run one against a real deployment:

```bash
BASE_URL=https://your-deployed-server.example LOAD=300 \
  make loadtest-remote SCENARIO=01_registration.js
```

**The two knobs that matter**: `BASE_URL` (any `lib/api.js` function reads
`__ENV.BASE_URL`, default `http://localhost:8080`) and `LOAD` (maps to
`VUS`, the candidate count every scenario reads via `__ENV.VUS`). Pass extra
scenario-specific env with `ENV="LANGUAGE=java DURATION=2m"`. For the full
certification run specifically, use `make loadtest-certify` instead (below)
— it wraps the same idea with a reproducibility manifest.

**This whole `loadtest/` (and `postman/`) directory is gitignored** —
nothing here is ever committed, so none of it can end up in a deployed build
regardless of how that deployment is triggered. Treat it as a local-only
toolbox that happens to point at a remote `BASE_URL`.

**`verify/` and `growth/` need direct Postgres access** (`DATABASE_URL` in
`.env`), which a production deployment typically does **not** expose
publicly (e.g. `docker-compose.prod.yml` never publishes the `db` service's
port). Options: run them from a host that already has DB access (SSH into
the deploy host, or as a one-off container inside its Docker network), or
temporarily expose the port for the duration of a verification pass and
close it again afterward, or skip them and rely on the k6 scenarios' own
in-script assertions — which already cover the large majority of each
test's correctness claims via HTTP-response checks, independent of DB
access. `verify`'s value-add is specifically the invariants no HTTP response
alone can prove (exact row counts, no duplicate/cross-candidate rows) — the
"Status" table below marks which scenarios lean on it.

## Prerequisites

- `brew install k6` (load generator)
- Go toolchain (already required by the main repo)
- Docker + the project's `docker-compose.yml` stack (`make compose-up`)
- Python 3, stdlib only (used for `monitor/report.py`)
- **Before any real capacity number means anything**, the Docker VM backing
  this repo's containers needs enough CPU/RAM, and — on Apple Silicon —
  Rosetta-backed amd64 emulation, or Piston's `isolate` sandbox fails every
  execution with `clone failed: Invalid argument` (`/me/run` will still 200,
  but every test case comes back `failed` with that error as the output, not
  a real result). What this actually is depends on your Docker provider —
  **check with `docker context ls`** before assuming Docker Desktop:
  - **Colima** (`docker context ls` shows `colima`): both are one `colima
    stop && colima start --cpu 6 --memory 8 --vz-rosetta` away (needs
    `vmType: vz` in `~/.colima/default/colima.yaml`, not the older `qemu`
    type). This does **not** wipe existing volumes/images — only `colima
    delete` does.
  - **Docker Desktop**: Settings → Resources (CPUs/Memory, well above the
    ~2 CPU/2GB default) and Settings → General → "Use Rosetta for x86/amd64
    emulation on Apple Silicon", then restart Docker Desktop.

  Confirm the Rosetta fix took with a direct Piston call:
  ```bash
  curl -s -X POST http://localhost:2000/api/v2/execute -H 'Content-Type: application/json' \
    -d '{"language":"python","version":"*","files":[{"name":"main.py","content":"print(6*7)"}]}'
  # expect {"run":{"stdout":"42\n", ...}}, not a clone/isolate error
  ```
  and the resource bump with `docker info --format 'CPUs={{.NCPU}} Mem={{.MemTotal}}'`.
  This dev machine runs Colima; both were fixed there via the commands above
  (6 CPUs / 8GB, `--vz-rosetta`) — see "Status" below for the confirmed result.

## Two request shapes — don't conflate them

- **Paced scenarios**: k6 VUs with realistic per-candidate think-time, not
  synchronized — models real candidate behavior. Used for core exam-taking
  flows and the realistic mixed-workload/certification scenarios.
- **Synchronized burst scenarios**: every VU fires within the same tick (e.g.
  `per-vu-iterations` with all VUs pre-allocated together) — used wherever the
  test is specifically about true simultaneity: registration storms, rate/
  concurrency-limit tests, autosave/heartbeat bursts.

"300 concurrent candidates" (paced) and "300 simultaneous requests" (burst)
are different claims. Each scenario file's header states which one it is.

## `__VU` is not a safe candidate-selection key in a multi-scenario file

Any scenario file that declares more than one entry under `options.scenarios`
and needs to map each VU to a specific pool candidate (`data.candidates[...]`)
**must** use `exec.scenario.iterationInTest` (from `import exec from
'k6/execution'`), never `(__VU - 1) % pool.length`. Confirmed empirically
with k6 v2.2.0: `__VU` is globally unique across a whole test, but it is
**not** allocated as clean, disjoint, contiguous blocks per scenario — not
even for scenarios that all start at the exact same instant. A 5-VU and
another 5-VU scenario starting together were observed getting fully
interleaved ranges like `{1,3,5,7,8}` and `{2,4,6,9,10}`. The practical
consequence: `(__VU-1) % pool.length` can duplicate one candidate while
silently skipping another — this dropped 1 of 50 candidates entirely from
`14_submit_storm.js`'s final submit (0 answers, never submitted, stuck
`active`) and from `27_mixed_realistic.js`'s final burst, and would have
made `09_run_concurrency_limit.js`'s "confirm the concurrency limit"
throttle count misleading (an apparent 429 that was really the *rate*
limiter firing on a duplicated candidate, not the concurrency queue).
`exec.scenario.iterationInTest` is scoped to one scenario and increments
exactly once per iteration with no gaps or reuse, regardless of what `__VU`
k6 happens to assign — every scenario file now uses it. Single-scenario
files are unaffected (there's no sibling scenario to interleave with), so
plain `__VU` stays fine there.

## Directory layout

```
k6/lib/          shared request wrappers (api.js), the expected-vs-failure
                 outcome classifier (checks.js), candidate/code fixtures (data.js),
                 shared exam/candidate setup (setup.js), cross-stage result
                 writing (summary.js)
k6/scenarios/    one file per test — see "Status" for what exists;
                 certification_run.js is the Phase 12 entry point
verify/          Go program: SQL-level invariant checks a load tool can't see
                 from HTTP responses alone (exact counts, no duplicate
                 sessions, cross-candidate leakage, score correctness, ...)
                 — needs direct Postgres access, see "Testing a deployed server"
growth/          Go program: bulk-inserts synthetic DB rows directly via SQL
                 (bypasses the API) to simulate accumulated DB size — also
                 needs direct Postgres access
chaos/           scripted failure injection (Postgres/API restart, Piston
                 stop/start), run_with_chaos.sh to orchestrate one against a
                 live k6 workload, UTC-timestamped logging for correlation
monitor/         poll.sh (docker stats + pg_stat_activity + API process
                 CPU/RSS -> CSV) and report.py (CSV -> self-contained HTML
                 report, including an N-way comparison for the tuning sweep
                 and the Level A vs Level B certification)
certify.sh       wraps certification_run.js with a reproducibility manifest
                 (git SHA, branch, params) for the Phase 12 certification runs
results/         gitignored; each run's k6 summary / monitor CSV / verify
                 output / chaos log / manifest lives here
```

This whole directory (plus `postman/`) is itself gitignored at the repo
root — see "Testing a deployed server" above.

## Running things

**Against a deployed server** (see "Testing a deployed server" above for the
full explanation):

```bash
BASE_URL=https://your-deployed-server.example LOAD=300 \
  make loadtest-remote SCENARIO=01_registration.js

# the full certification run, with a reproducibility manifest:
make loadtest-certify BASE_URL=https://your-deployed-server.example LOAD=300 RUN_ID=cert300-20260906-01
```

**Against a local dev server** (for iterating on the harness itself):

```bash
make compose-up && make runtimes && make seed   # infra, once
make loadtest-server-start                       # API with a tracked PID
make loadtest-monitor-start                       # background resource polling

# a single scenario (RESULTS_DIR must exist first — k6 can't create it,
# defaults to loadtest/results/latest which the repo scaffolding creates):
VUS=300 k6 run loadtest/k6/scenarios/01_registration.js

# verify DB-level invariants for the exam that scenario created (see its
# console output for EXAM_ID=...):
EXAM_ID=<id> EXPECTED=300 make loadtest-verify CHECK=all

make loadtest-monitor-stop
make loadtest-server-stop
```

**Smoke test the harness itself before trusting any real number from it**
(small scale, proves the whole pipeline is wired correctly):

```bash
make loadtest-server-start
make loadtest-smoke
make loadtest-server-stop
```

### Chaos scripts

Each `chaos/*.sh` performs one failure-injection action and logs a UTC
timestamp. `restart_postgres.sh` and `stop_piston.sh`/`start_piston.sh`
control this repo's own `docker-compose.yml` containers, so they only make
sense against **local infra you control** — for chaos-testing a deployed
server, trigger the equivalent action through however that deployment
manages its containers (Dokploy, etc.) instead, timed by hand or by
adapting `orchestrate.sh`'s wait-then-fire pattern. `restart_api.sh` is
local-only for the same reason (it manages the pidfile from
`make loadtest-server-start`).

`chaos/run_with_chaos.sh <scenario.js> <delay_sec> <chaos_script.sh> [args]`
starts a k6 scenario in the background and fires one chaos action partway
through it, then waits for k6 to finish — this is what actually exercises a
chaos script against a live, in-flight workload rather than testing it
standalone. `26_sustained_activity.js` is the general-purpose target
workload for this (paced paper-load/autosave/heartbeat, plus periodic
`/me/run` calls when `INCLUDE_RUN_CODE=true` — needed for the Piston chaos
test specifically); `14_submit_storm.js` is the target for the
API-restart-during-grading variant, since it needs grading genuinely in
flight when the restart fires. Example (local infra):

```bash
make loadtest-server-start
VUS=10 DURATION=45s bash loadtest/chaos/run_with_chaos.sh 26_sustained_activity.js 15 restart_postgres.sh
VUS=10 DURATION=45s bash loadtest/chaos/run_with_chaos.sh 26_sustained_activity.js 15 restart_api.sh
VUS=10 DURATION=45s INCLUDE_RUN_CODE=true bash loadtest/chaos/run_with_chaos.sh 26_sustained_activity.js 15 stop_piston.sh
bash loadtest/chaos/start_piston.sh   # restart it afterward
VUS=10 POLL_INTERVAL_SEC=3 MAX_POLL_SEC=90 bash loadtest/chaos/run_with_chaos.sh 14_submit_storm.js 3 restart_api.sh
```

A chaos run is **expected** to show some failed checks during the outage
window itself (that's the disruption actually happening) — `26_sustained_
activity.js` deliberately carries no thresholds, so a k6 exit code alone
isn't the pass/fail signal here. What matters: (1) requests succeed again
once the action completes (recovery), and (2) `loadtest/verify` afterward
shows no corruption (use the checks relevant to what that target scenario
actually touches — `26_sustained_activity.js` only ever answers the coding
question, so its `answers` count is `1 x sessions`, not `3 x sessions`; see
the Status table's Phase 8 note).

### Reading `monitor/report.py`'s output

```bash
python3 loadtest/monitor/report.py --run "run=loadtest/results/monitor.csv" --out loadtest/results/report.html
```

Pass `--run` twice (e.g. a 200-candidate run and a 300-candidate run using
the same certified config) to get a side-by-side comparison table instead of
a single report — this is how the Level A/B headroom argument gets made.

## Pass/fail criteria

| Metric | Target at 300 |
|---|---|
| Candidate registration | ≥99.5% successful |
| Paper loading | ≥99.9% successful |
| Answer saves | ≥99.9% successful |
| Heartbeats | ≥99.9% successful |
| Normal API requests | ≥99% successful |
| Unexpected 5xx | <0.1% |
| DB errors / lost answers / duplicate sessions / incorrect set assignments / premature or missed auto-submit / incorrect scores / data corruption | 0 |
| Code-run requests | No crashes; intentional 429s acceptable and excluded from error rate |
| Grading | Every submitted candidate eventually reaches `graded` (latency measured, not assumed instant — see below) |
| Restart recovery | No lost submissions/grading jobs |
| API p95 latency (normal endpoints) | <500ms |
| API p99 latency | <2s |
| Exam completion | 300/300 eventually finalized correctly |
| **Candidate-facing outage** | No candidate-facing request may fail because the app is overloaded — throttling `/me/run` must never degrade any other endpoint |

**Expected vs. failure** (enforced by `k6/lib/checks.js`'s `classify()`, not by convention):

| Outcome | Classification |
|---|---|
| 2xx | OK |
| `429` from `/me/run` specifically | Expected (intentional throttling) — tracked separately, never an error |
| `429` from anywhere else, or any `5xx`, connection reset/refused, or an unanswered request past its timeout | Failure |

**Grading is measured two ways**: end-to-end submit→graded latency per
candidate (p50/p95/p99/max), plus a point-in-time census of how many sessions
are still in `grading` at fixed checkpoints (5/10/20 min). The operational SLA
ceiling is decided from that data once it exists, not guessed in advance.

## Status

Phase 0 (making `RUN_CONCURRENCY`/`GRADER_WORKERS`/`RUN_QUEUE_TIMEOUT_SEC`/
`RUN_RATE_LIMIT_SEC` configurable) and Phase 1 (this scaffolding — libs,
verify tool, monitor/chaos scripts, Makefile targets, `lib/setup.js`'s shared
exam-setup/candidate-registration helpers) are done.

Phase 2 (infra pre-flight) is **done**. Postgres/Piston/migrations/routing all
confirmed healthy; this dev machine's Docker VM (Colima) was reconfigured
from the default 2 CPUs/2GB with Rosetta disabled to 6 CPUs/8GB with
`--vz-rosetta` enabled (`colima stop && colima start --cpu 6 --memory 8
--vz-rosetta`) — confirmed non-destructive (all volumes/exams survived) and
confirmed fixed with a direct Piston execute call (`print(6*7)` now returns
`stdout: "42\n"` instead of `clone failed: Invalid argument`). The idle
resource baseline was recaptured after the fix at
`loadtest/results/phase2-baseline/idle_monitor.csv`: DB ~0% CPU/57MB, Piston
~0-5% CPU/123MB, API ~0% CPU/24MB RSS, 2 idle pg connections. `make
loadtest-smoke` re-confirmed clean at the new allocation.

Phase 3 (candidate/API load) is **done** for its 7 scenarios below — each
verified with a real `k6 run` at VUS=10 plus the matching `verify` check(s)
passing against a live server (this dev machine, post Phase-2-fix). Along the
way, building these caught and fixed three harness bugs worth knowing about:
`k6 run ... | tee` needs `2>&1` (k6's `console.log` goes to stderr, not
stdout); a scenario's `RESULTS_DIR` must exist before k6 runs (it can't
create directories itself — `loadtest/results/latest/` is now tracked via
`.gitkeep` so a fresh clone has it); and passing data from `setup()` into
`handleSummary()` requires a pre-declared k6 metric (`lib/summary.js`'s
`recordMeta`/`readMeta`), not a plain module-level variable — k6 gives each
execution stage its own fresh module instantiation, so simple JS state
doesn't carry across.

**Run the checks relevant to the scenario you ran, not blindly
`--check=all`** — e.g. `06_violations.js` never touches answers, so its
`answers` check correctly reports `answers=0` and is not a bug; only the
full certification run (Phase 12) is expected to exercise every check at once.

Phase 4 (code-execution stress) is **done** for its 7 scenarios below, each
verified at small scale (3-12 VUs) against a live server, including a
deliberate tight-limits run (`RUN_CONCURRENCY=1 RUN_QUEUE_TIMEOUT_SEC=1`) that
confirmed the `throttled_run` classifier correctly buckets 429s as expected
rather than errors. Two real Piston-behavior findings surfaced while building
`12_run_compile_fail.js` (not bugs in the app — this Piston deployment's
actual observed behavior, worth knowing before writing more run-code
scenarios): **`compileOk` is not reliably `false` for broken code** — Python
has no separate compile stage at all (a `SyntaxError` surfaces as a failed
test's `actual`, `compileOk` stays `true`), and this deployment's `javac`
package likewise reports `compileOk:true` with the compiler error in
`actual` rather than short-circuiting (only the `gcc`/C package here actually
sets `compileOk:false`). And **when `compileOk` genuinely is `false`, the API
returns `results: []`** (empty — it never runs any test case, by design in
`internal/handlers/student_run.go`), which is the correct shape, not a
missing-data bug. The robust, environment-accurate assertion for "broken code
didn't get falsely marked as passing" has to account for both shapes — see
the comments in `12_run_compile_fail.js`.

**Important correctness fix, found while building Phase 5** (retroactively
affects every scenario above that uses `lib/setup.js`'s `setupExam()` — i.e.
all of Phase 3 and 4): `setupExam()` originally populated every set via
`POST .../auto-distribute`, but auto-distribute samples questions from the
**entire** `questions` table, not just the ones a given scenario run just
created — after enough scenario runs accumulate questions in the shared
bank (42 coding questions by the time this was caught), `mode: shuffled`
started silently assigning an unrelated pre-existing question instead of the
one `setupExam()` had just built. This stayed invisible for every prior
scenario purely because every fixture question happens to share the same
shape (4 test cases, 2 hidden, weight 1 each) — it surfaced as a real,
visible failure only in `16_partial_scores.js`, whose custom-weighted
question got swapped out from under it. Fixed by switching `setupExam()` to
explicit `PUT /admin/sets/:id/questions` with the exact question IDs it just
created (same fix already applied in `postman/exam-taker.postman_collection.json`
earlier — this is the second time this exact class of bug showed up, so
watch for it in any future harness code that populates sets). Re-verified
clean against scenarios 01 and 02 after the fix; all of Phase 3/4's pass/fail
conclusions stand (uniform fixtures meant they were accidentally correct),
but their question-assignment determinism is only actually guaranteed now.

Phase 5 (submission/grading capacity) is **done** for its 3 files below
(covering Tests 14, 15, 16, 18, 19). `14_submit_storm.js` implements the
plan's two-sided grading measurement: `submit_to_graded_ms` (a Trend metric,
so p50/p95/p99/max come straight out of the k6 summary) plus a
`CENSUS t+Ns active=.. grading=.. graded=.. total=..` log line every
`CENSUS_INTERVAL_SEC` — grep it for the line closest to t+300/600/1200 to
answer "how many still grading at 5/10/20 min" at real scale. Building it
also caught a second `__VU`-indexing bug (see `09_run_concurrency_limit.js`'s
lesson, forgotten and reintroduced here): with two scenarios running
concurrently (`submit_and_wait` + `grading_census`), `__VU` numbering
shifted and `data.candidates[__VU - 1]` went out of bounds for one
candidate — fixed with the same `% pool.length` wrap.

Phase 6 (time & race conditions) is **done** for its 6 scenarios below. Two
things worth knowing before writing more deadline/grace-boundary tests: **not
every write endpoint has a grace-check** — `sessionWritable()`
(`internal/handlers/student_answers.go`) is what enforces `endsAt +
ANSWER_GRACE_SEC` and it's only wired into `PUT /me/answers` and
`POST /me/run`; `POST /me/submit` has none at all (it just races
`FinalizeSession`'s atomic update against whichever sweeper tick gets there
first — either outcome, `manual` or `auto_time`, is correct), and
`POST /me/heartbeat` has no grace-check either and always returns 200
regardless of timing. So `18`/`19` are genuinely probabilistic sweeper races
(rerun them a few times — a given run may land all-`manual` or a mix,
both are valid) while `20`'s answer-save assertions are deterministic. And
**`endsAt` is serialized inconsistently**: UTC (`...Z`) on a fresh
registration, the server's local offset (`...+05:30`) on a resumed one —
same instant, different valid RFC3339 strings. `21_resume.js` originally
compared these as raw strings and failed 100% of the time despite nothing
being wrong; fixed by comparing `new Date(...).getTime()` instead, which is
also the correct way for any real client to do this comparison.

Phase 7 (admin workload) is **done** for its 2 scenarios below.
`24_admin_monitoring.js` asserts the "monitoring doesn't degrade candidates"
claim directly via k6 thresholds (`p(95)<500ms` on the `paper`/`answer`/
`heartbeat`-tagged requests, matching the B.2 normal-endpoint target) while
two admin polling loops run continuously alongside real candidate traffic,
rather than leaving it as an eyeballed "seems fine."

**From here on (Phase 8 onward), everything below is built but NOT run
locally** — testing moved to a deployed server instead of the local dev
stack partway through building this harness (see "Testing a deployed
server" above), so these scenarios have had the same careful design/code
review as everything above but not yet a real execution against a live
server. Expect the first real runs to surface something, the way every
earlier phase did — that's not a sign anything here is worse-written, it's
just what a first real run against real infrastructure always finds
regardless of how carefully the code was reviewed beforehand.

**Phase 8 (failure/chaos recovery)** is built (`chaos/run_with_chaos.sh`,
`26_sustained_activity.js` as the general chaos-target workload) and
**partially run against local infra** before the pivot: Postgres-restart and
plain API-restart were both fired mid-workload and confirmed recovering
cleanly (a brief window of `errors_network` exactly during the outage, zero
data loss/duplication afterward per `verify --check=registrations`). The
API-restart-**during-grading** variant (which specifically exercises
`RequeueStuck()`) was started but not completed before switching to the
deployed-server workflow — rerun it as:
`VUS=10 POLL_INTERVAL_SEC=3 MAX_POLL_SEC=90 bash loadtest/chaos/run_with_chaos.sh 14_submit_storm.js 3 restart_api.sh`.
Piston stop/start was unit-tested standalone (Phase 1) but never yet
orchestrated against a live in-flight workload — pair `stop_piston.sh`/
`start_piston.sh` with `26_sustained_activity.js`'s `INCLUDE_RUN_CODE=true`
per the chaos-scripts section above.

**Phase 9 (tuning sweep & runner fallback)** needs no new scenario files —
it's a procedure reusing `09_run_concurrency_limit.js` (for the
`RUN_CONCURRENCY` sweep) and Phase 4's run-code scenarios (for the
`RUNNER=docker` fallback rerun), against the SAME deployed server
reconfigured between runs with different `RUN_CONCURRENCY`/`GRADER_WORKERS`/
`RUNNER` values — that reconfiguration has to happen on however the
deployment is managed (Dokploy env vars, etc.), not from here. For each
sweep value, run `09_run_concurrency_limit.js` and label the resulting
`monitor.csv` (if resource polling is available for that deployment) for
`monitor/report.py --run "RUN_CONCURRENCY=4=..." --run "RUN_CONCURRENCY=8=..." ...`
— it accepts more than two `--run` flags, not just a pair, so the whole
sweep renders as one comparison table. Runner-selection verification
(`auto`/`piston`/`docker`, broken-Piston fallback) means restarting the
deployed server with each `RUNNER` value and checking its startup log for
the exact lines `internal/runner/select.go` emits (`"runner: piston OK at
%s"`, `"runner: using docker-exec container %q"`, or the "staying on
piston" warning if neither works) — a log-reading checklist, not something
runnable from here without access to that log.

Everything below was built after the pivot to deployed-server testing (see
above) — nothing past this point has run against ANY server, local or
deployed.

| Scenario | Status | File / verify check |
|---|---|---|
| 300 registrations (synchronized burst) | **done** | `01_registration.js` · `verify --check=registrations,sets` |
| 300 load paper (+ security: no answer key / hidden tests leaked) | **done** | `02_paper_load.js` · security assertions are in-scenario k6 checks |
| 300 autosave (all answer types, changes, clears) | **done** | `03_autosave.js` · `verify --check=answers` |
| Rapid autosave burst (5x, 10x) | **done** | `04_autosave_burst.js` (`BURST_SIZE` env) · `verify --check=answers` |
| Heartbeat storm | **done** | `05_heartbeat_storm.js` (`ROUNDS`/`ROUND_GAP_SEC` env) |
| DB connection-exhaustion (heartbeat+autosave+admin polling+normal, concurrently) | **done** | `05b_connection_pressure.js` — pair with `INTERVAL_SEC=1 monitor/poll.sh` |
| Violations incl. 3-strike auto-submit | **done** | `06_violations.js` · `verify --check=violations` |
| Run code — Python/C/Java/mixed | **done** | `07_run_code.js` (`LANGUAGE=python\|java\|c\|mixed` env) |
| Per-session rate limit | **done** | `08_run_rate_limit.js` |
| Global concurrency limit (the `RUN_CONCURRENCY` sweep target) | **done** | `09_run_concurrency_limit.js` — includes a concurrent heartbeat/answer-save health-traffic pool |
| Timeout / infinite loops | **done** | `10_run_timeout.js` — slow at 300 scale by design (3s timeLimitMs x queueing), see file header |
| Memory-heavy programs | **done** | `11_run_memory.js` (`MEMORY_LIMIT_KB` env, default 64MB) |
| Compilation failures | **done** | `12_run_compile_fail.js` — see the Piston-behavior notes above |
| Custom stdin sizes/shapes | **done** | `13_run_custom_stdin.js` — includes the >10000-char rejection boundary |
| 300 simultaneous submits | **done** | `14_submit_storm.js` (`MAX_POLL_SEC` env — raise for real scale) |
| Grading queue at max pressure (3,600 executions) | **done** | reuses `14_submit_storm.js`'s data · `verify --check=grading` |
| Partial coding scores (weighted) | **done** | `16_partial_scores.js` — dedicated 1+1+3+5 weighted question |
| English manual grading, concurrent | **done** | `17_manual_grading.js` — cross-candidate contamination check |
| Time expiry / sweeper | **done** | `18_time_expiry.js` · `verify --check=time_expiry` — takes ~2min by design (1min exam + 30s grace + sweeper tick lag) |
| Deadline + submit race | **done** | `19_deadline_submit_race.js` — races the sweeper via jitter around endsAt+grace |
| Deadline + autosave/heartbeat race | **done** | `20_deadline_autosave_race.js` — deterministic (sessionWritable's grace-check), not a sweeper race — see file header |
| Resume (concurrent re-register, same email) | **done** | `21_resume.js` |
| Duplicate registration race | **done** | `22_duplicate_registration_race.js` — true concurrency via `http.batch()`, not sequential |
| Already-submitted -> 409 | **done** | `23_already_submitted.js` |
| Admin monitoring polling during exam | **done** | `24_admin_monitoring.js` — p95 latency thresholds prove no candidate-facing degradation |
| CSV export under load | **done** | `25_csv_export.js` — exports both mid-grading and after, exact row-count check |
| Postgres restart mid-workload | **verified locally** | `chaos/run_with_chaos.sh 26_sustained_activity.js <delay> restart_postgres.sh` |
| API restart mid-workload | **verified locally** | `chaos/run_with_chaos.sh 26_sustained_activity.js <delay> restart_api.sh` |
| API restart during grading (`RequeueStuck`) | built, run interrupted before completion | `chaos/run_with_chaos.sh 14_submit_storm.js <delay> restart_api.sh` |
| Piston stop/restart mid-run | built, not yet orchestrated live | `chaos/run_with_chaos.sh 26_sustained_activity.js <delay> stop_piston.sh` (`INCLUDE_RUN_CODE=true`) + `start_piston.sh` |
| `RUN_CONCURRENCY` / `GRADER_WORKERS` sweep, `RUNNER=docker` fallback, runner-selection check | documented procedure, no new files | reruns `09_run_concurrency_limit.js` / Phase 4 scenarios against a reconfigured deployment — see Phase 9 note above |
| Realistic mixed workload + synchronized spikes | **built, not run** | `27_mixed_realistic.js` |
| Multi-exam activation (Exam A candidates survive Exam B activating) | **built, not run** | `28_multi_exam_activation.js` |
| Question edit/delete mid-exam (documents actual cascade behavior) | **built, not run** | `29_question_mutation_mid_exam.js` |
| DB growth (10k/100k/1M answers) + before/after latency | **built, not run** | `growth/main.go` (direct-SQL bulk insert, bypasses the API) — pair with timed `curl` calls before/after |
| Progressive ramp 10→350 | documented procedure, no new file | repeat `make loadtest-remote SCENARIO=... LOAD=10\|25\|50\|100\|150\|200\|250\|300\|350`, then `monitor/report.py` with one `--run` per step (accepts more than two, renders all as one table) |
| **Final certification — Level A (300) / Level B (200)** | **built, not run** | `certification_run.js` (full realistic exam + concurrent admin monitoring + CSV export) via `make loadtest-certify` — run 3x each as `cert300-YYYYMMDD-NN` / `cert200-YYYYMMDD-NN`, compare with `monitor/report.py` |
