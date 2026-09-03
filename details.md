# Creo Assess Backend — Detailed Reference

This is the deep-dive companion to [README.md](README.md): full architecture, data model, feature behavior, and a complete API reference for `exam_taker_bc`, the Go backend powering Creo Assess — a proctored mock-test platform (English / Aptitude MCQ / Coding questions, timed exams, sandboxed code execution, anti-cheat auto-submit, async grading, CSV export).

## Table of contents

1. [Architecture overview](#1-architecture-overview)
2. [Data model](#2-data-model)
3. [Authentication & authorization](#3-authentication--authorization)
4. [Feature deep-dives](#4-feature-deep-dives)
5. [Complete API reference](#5-complete-api-reference)
6. [Configuration reference](#6-configuration-reference)
7. [Local development & infra](#7-local-development--infra)
8. [Production deployment](#8-production-deployment)
9. [Testing](#9-testing)
10. [Known limitations](#10-known-limitations)

---

## 1. Architecture overview

**Stack**: Go 1.27, [Gin](https://github.com/gin-gonic/gin) (HTTP), [GORM](https://gorm.io) + PostgreSQL (persistence), [Piston](https://github.com/engineer-man/piston) (sandboxed code execution) with a native `docker exec` fallback, JWT (`golang-jwt/jwt/v5`) for admin auth, bcrypt for password hashing, opaque UUID tokens for candidate sessions.

### Package layout

```
cmd/
  server/main.go     — wires everything and starts the HTTP server
  seed/main.go        — idempotent demo-data seeder (calls the same EnsureAdmin + seed package)
internal/
  config/              — env var loading (godotenv + os.Getenv, single source of truth for defaults)
  database/            — DB connect, AutoMigrate, EnsureAdmin (bootstraps the first admin account)
  models/              — all GORM entities + shared enums/constants (single models.go)
  middleware/          — AdminAuth (JWT) and StudentAuth (opaque session token) Gin middleware
  handlers/            — HTTP layer: router.go + one file per resource area (admin_auth, admin_questions,
                          admin_exams, admin_sessions, student, student_answers, student_run, helpers)
  services/            — business logic with no HTTP awareness: assignment.go (set picking),
                          distribute.go (auto-distribute), grading.go (the Grader worker pool),
                          submit.go (FinalizeSession + the time-based sweeper)
  runner/               — code-execution abstraction: runner.go (interface), piston.go (HTTP client),
                          docker.go (docker-exec fallback), select.go (auto-probe logic)
  seed/                 — one-time demo content (13 questions + 1 activated exam)
runner-image/           — Dockerfile for the native-arch fallback runner container
scripts/install-runtimes.sh — one-time script that installs python/gcc/java into a running Piston container
postman/                — Newman/Postman regression collection covering the whole API (see §9)
```

Handlers are thin: they parse/validate the request, call into `internal/services` or do simple GORM queries directly, and shape the JSON response. All business rules that matter (set assignment, grading, auto-submit, distribution) live in `internal/services` and are unit-tested independently of HTTP (`internal/services/services_test.go`).

### Process model

`cmd/server/main.go` does, in order:

1. Load config from `.env` / environment (`config.Load()`).
2. Open the Postgres connection pool (max 20 open / 5 idle conns, 1h conn lifetime) and run `AutoMigrate` for all 10 models.
3. `EnsureAdmin` — creates the admin account from `ADMIN_EMAIL`/`ADMIN_PASSWORD` if no admin with that email exists yet (bcrypt-hashed, idempotent).
4. `runner.Select(...)` — picks the code runner (see [§4.5](#45-code-execution-piston--docker-fallback)).
5. Construct the `Grader` and start **`GRADER_WORKERS` background workers** (default 4) consuming a buffered channel (capacity 1024) of session IDs to grade.
6. `grader.RequeueStuck()` — re-enqueues any session left in `grading` status (e.g. the process crashed mid-grade on a previous run).
7. `services.StartSweeper(...)` — a goroutine that ticks every 30s and force-submits any `active` session whose `ends_at + ANSWER_GRACE_SEC` has passed, so a closed laptop / dead client can't keep a session open forever.
8. Build the Gin router and start listening on `PORT`.

There is no separate worker process or queue system (no Redis/RabbitMQ) — grading and the sweeper are in-process goroutines backed by a Go channel and a ticker. This is intentionally simple and fine at the scale of a single-exam-at-a-time college test platform; it does mean grading throughput and the sweeper both die if the process dies (mitigated by `RequeueStuck` on the next boot, since state lives in Postgres, not the queue).

### Request lifecycle (typical candidate write)

```
Client → Gin router → CORS middleware → StudentAuth middleware (resolves Bearer token
  → ExamSession row, preloading Exam + Student) → handler (validates payload, checks
  session is writable) → GORM → Postgres
```

For `POST /me/run` specifically, the handler also calls out synchronously to the `Runner` (Piston HTTP API or a `docker exec` into the fallback container) before responding — this is the one endpoint with real external latency and the only one that's rate-limited (see [§4.6](#46-run-code--rate-limiting)).

---

## 2. Data model

All models are defined in `internal/models/models.go` and auto-migrated (no separate migration files — GORM's `AutoMigrate`). PostgreSQL, but `gorm.io/driver/sqlite` is also imported (available for tests/local experimentation, not used by the running server).

| Table | Purpose | Key relationships |
|---|---|---|
| `admins` | Login for the admin/staff panel | — |
| `questions` | The question bank (english / aptitude / coding) | has many `test_cases` |
| `test_cases` | Coding-question test cases (input/expected/hidden/weight) | belongs to `questions` |
| `exams` | An exam definition (title, duration, max violations, status) | has 6 `question_sets` (A–F) |
| `question_sets` | One labeled paper variant per exam | belongs to `exams`; has many `set_questions` |
| `set_questions` | Join table: which questions (in what order, worth how many marks) are on a given set | belongs to `question_sets` + `questions` |
| `students` | A candidate's identity (name/email/semester/phone) | unique on `email` |
| `exam_sessions` | One candidate's attempt at one exam | belongs to `students` + `exams` + (assigned) `question_sets`; unique on `(exam_id, student_id)` |
| `answers` | One saved answer per `(session, question)` | belongs to `exam_sessions`; unique on `(session_id, question_id)` |
| `violations` | Proctoring event log | belongs to `exam_sessions` |

### Notable field-level details

- `Question.Options` / `Question.StarterCode` are stored as JSON columns (GORM `serializer:json`), not normalized tables — simple and fine since they're never queried by content.
- `Question.CorrectIndex` is a `*int` (pointer) — this is stripped out of every student-facing response so candidates can't inspect the answer key in the browser network tab.
- `Answer.SelectedIndex` is also `*int` — `null` means "unanswered", `0` is a valid answer (selecting the first option). Callers must not conflate the two.
- `ExamSession.Token` is an opaque UUID string, unique-indexed, and is the entire authentication mechanism for candidates (see [§3](#3-authentication--authorization)) — it's excluded from JSON (`json:"-"`).
- `ExamSession` has a unique index on `(exam_id, student_id)` — a student can have at most **one** session per exam ever, which is what makes the "resume vs. already-attempted" logic in `POST /sessions` possible (see [§4.3](#43-candidate-registration--resume-semantics)).
- `Answer.TestResults` (`[]TestResult`, JSON column) stores the full per-test-case grading detail (status, actual output, weight) for admin review — this is what the grading dashboard reads to show why a coding answer scored what it did.

---

## 3. Authentication & authorization

Two completely independent schemes, both passed the same way — HTTP header `Authorization: Bearer <token>` — but never interchangeable.

### 3.1 Admin JWT

- Issued by `POST /api/v1/admin/login` after a bcrypt password check against the `admins` table.
- HS256, signed with the `JWT_SECRET` env var.
- Claims: `sub` (admin ID), `role: "admin"`, `iat`, `exp` = issued-at + **24 hours**. No refresh-token endpoint — once it expires, log in again.
- `middleware.AdminAuth(secret)` verifies the HMAC signature, checks `role == "admin"`, and sets `adminID` in the Gin context. Applied to the entire `/api/v1/admin` group except `POST /admin/login` itself.
- Stateless — there's no server-side revocation/logout endpoint. Rotating `JWT_SECRET` is the only way to invalidate all admin tokens at once.

### 3.2 Candidate session token

- **Not a JWT** — an opaque `google/uuid` v4 string, minted and returned by `POST /api/v1/sessions` (the candidate "register/join" call), and stored as `exam_sessions.token`.
- `middleware.StudentAuth(db)` looks the token up directly against `exam_sessions.token` (with `Exam` and `Student` preloaded) — a database read on every authenticated candidate request, not a cryptographic check. There's no expiry embedded in the token itself; time-boxing is enforced separately via `ends_at` + grace-period checks inside each write handler.
- Applied to the entire `/api/v1/me/*` group.
- There is **no password-based candidate login** at all. Identity is just "whoever knows this session token", handed out once at registration and expected to live in the exam frontend's client-side storage for the duration of the attempt.

---

## 4. Feature deep-dives

### 4.1 Question bank

Three question types, one `questions` table:

- **English** — a markdown `body` (passage + prompt) and free-text `answerText`; always graded manually by an admin.
- **Aptitude (MCQ)** — `options []string` (≥2, all non-empty) + `correctIndex`; graded automatically by exact index match.
- **Coding** — `starterCode` per language (`python`/`java`/`c`), a `syntaxNote`, `timeLimitMs` (clamped 1–10000ms, default 3000), optional `memoryLimitKb`, and `testCases` (at least one **non-hidden** "sample" case required at creation, so students always see at least one example). Test cases can be marked `hidden` (excluded from the student-facing paper and from `/me/run` sample mode, but always used at grading time) and given a `weight` for partial credit.

Full CRUD lives under `/api/v1/admin/questions`. Deleting a question cascades its test cases and any `SetQuestion` links — there's no guard preventing deletion of a question currently in use by a live exam's set, so admins should be careful deleting questions after an exam has been activated.

### 4.2 Exam lifecycle & question sets (A–F)

Every exam is created with **exactly six** `QuestionSet` rows, labeled `A` through `F`, so different candidates sitting side-by-side can be given differently-ordered/sampled papers to make copying harder. An exam has a `status`: `draft → active → closed`.

Sets can be populated two ways:

1. **Manually**, one set at a time: `PUT /admin/sets/:id/questions` with an ordered `questionIds` array — array order becomes each question's `position` on that set, and `marks` is copied from the question's *current* marks at assignment time (so editing a question's marks later doesn't retroactively change already-assigned set marks).
2. **`POST /admin/exams/:id/auto-distribute`** with `{english, aptitude, coding, mode}` counts:
   - `mode: "unique"` — every set gets **different** questions of each type. Requires a bank of at least `count × 6` per type; questions are shuffled once and sliced into 6 disjoint blocks.
   - `mode: "shuffled"` — every set gets the **same** sampled questions, just shuffled into a different order per set. Requires only `count` questions per type in the bank.
   - Section order within a set is always **English → Aptitude → Coding**, regardless of mode.
   - This replaces the *entire* contents of all 6 sets (deletes existing `SetQuestion` rows for the exam first, inside a transaction).

**Activation** (`POST /admin/exams/:id/activate`) requires every one of the 6 sets to have ≥1 question, and — critically — **auto-closes every other currently-active exam**. Only one exam can be live platform-wide at a time; `GET /exam/active` and `POST /sessions` both implicitly target "whichever exam has `status = active`" with no exam ID in the request. `POST /admin/exams/:id/close` has no such guard.

**Deletion** (`DELETE /admin/exams/:id`) only succeeds while `status == "draft"` — once activated (even after later closing it), an exam is permanent history and can only be closed, never deleted. This is deliberate: closed exams carry real candidate results.

### 4.3 Candidate registration & resume semantics

`POST /api/v1/sessions` takes `{name, email, semester (1–8), phone?}` — no exam ID; it always targets whichever exam is currently `active`. The student is found-or-created by email (updating name/semester/phone on repeat). Then, per the unique `(exam_id, student_id)` constraint on `exam_sessions`:

| Existing session for this student+exam | Behavior |
|---|---|
| None | **201** — creates a new session: assigns the least-filled set via `services.AssignSet` (random tie-break among the least-used sets, so candidates spread evenly across A–F), starts the clock (`startedAt = now`, `endsAt = now + durationMin`), returns a fresh token. |
| `active`, and still within `endsAt + ANSWER_GRACE_SEC` | **200**, `resumed: true` — returns the **same** token and original `endsAt`. This is what lets a candidate refresh the page or reopen the tab mid-exam without losing progress. |
| `active`, but past `endsAt + ANSWER_GRACE_SEC` | Auto-finalizes the session server-side (`SubmitAutoTime`) first, then **409** `"your exam time is over — the attempt was submitted automatically"`. |
| Any non-`active` status (`grading`/`graded`) | **409** `"this email has already attempted the exam"` — no re-entry once submitted, by any path (manual, time-out, or violation limit). |

### 4.4 Autosave answers

`PUT /api/v1/me/answers/:questionId` is an upsert (`ON CONFLICT` on the unique `(session_id, question_id)` index) — there's deliberately no separate "create" endpoint, since the frontend autosaves on every change. The question must belong to the candidate's assigned set (404 otherwise). Payload shape depends on question type (`answerText` / `selectedIndex` / `code`+`language`), and every write is rejected with 409 once the session is no longer writable (submitted, or past the grace deadline — which, same as registration, triggers a server-side auto-finalize as a side effect of the rejected write).

### 4.5 Code execution: Piston & docker fallback

`internal/runner.Runner` is a small interface (`Execute(ctx, language, code, stdin, timeLimitMs, memoryLimitKb) (ExecResult, error)`) with two implementations:

- **`piston.Client`** — POSTs to `<PISTON_URL>/api/v2/execute`. Maps `python → main.py`, `java → Main.java` (the class **must** be named `Main`), `c → main.c`; always requests `version: "*"` (whatever's installed). `run_timeout` is the question's `timeLimitMs`; `run_memory_limit` is `memoryLimitKb * 1024` bytes if set, else `-1` (unlimited). A Piston `signal: "SIGKILL"` on the run stage is mapped to `TimedOut: true`.
- **`docker.DockerRunner`** — a native-arch fallback: builds the same source file inside `runner-image/` and drives it via `docker exec` into the `RUNNER_CONTAINER` (non-root, no network, memory/pids caps, kill timeouts). Exists because Piston's `isolate` sandbox needs true Linux namespaces and **cannot run under plain `qemu` amd64 emulation on Apple Silicon** — only under Rosetta-backed translation (Docker Desktop's "Use Rosetta" setting, or Colima's `--vz-rosetta` with `vmType: vz`). On a machine where neither Rosetta nor a native amd64 host is available, this fallback keeps local dev usable.

`internal/runner/select.go` (`RUNNER` env: `auto` | `piston` | `docker`) implements the choice: in `auto` mode (the default), it probes Piston with a real `print(6*7)` Python execution, up to 8 attempts over ~30s (Piston usually boots slower than the API server). If Piston answers but can't actually execute (broken sandbox), it stops probing immediately and falls through to a docker-exec health check instead of wasting the remaining retries. If neither works, it stays on the Piston client anyway (so the app still starts — code runs just fail with a clear 502 until infra is fixed) rather than crashing the whole server over an optional dependency.

Runtimes are **not** bundled in the Piston image — `scripts/install-runtimes.sh` must be run once against a live Piston container to install the latest `python`, `gcc` (covers C), and `java` packages via Piston's own `/api/v2/packages` install API. This is infra setup, not an application endpoint.

### 4.6 Run code & rate limiting

`POST /api/v1/me/run` is the only endpoint that talks to the runner synchronously and the only rate-limited one:

- **1 run per `RUN_RATE_LIMIT_SEC` seconds per session** (default 2) — a second call within the window gets `429` with a `Retry-After` header matching the actual remaining wait.
- **Max `RUN_CONCURRENCY` concurrent runs process-wide** (default 4) — additional requests queue for up to `RUN_QUEUE_TIMEOUT_SEC` (default 15s) before also getting a `429`. This caps load on Piston/docker regardless of how many candidates are running code simultaneously. All four values (`RUN_CONCURRENCY`, `RUN_RATE_LIMIT_SEC`, `RUN_QUEUE_TIMEOUT_SEC`, plus the grader's `GRADER_WORKERS`) are env-configurable specifically so load testing can find the real capacity ceiling empirically rather than by guessing — see `internal/handlers/student_run.go`'s `RunLimiter`.
- Two modes: `"samples"` (runs against up to 10 non-hidden test cases, stopping early with an empty result set if compilation fails) and `"custom"` (arbitrary `stdin`, returns raw `stdout`/`stderr`/`exitCode`/`timedOut`). Neither mode touches hidden test cases or persists a score — that only happens at grading time via `POST /me/submit`.

### 4.7 Proctoring & violations

`POST /api/v1/me/violations` is the single anti-cheat endpoint (there's no webcam/screenshot capture — this is browser-signal-based deterrence, not device-level proctoring). Two buckets, defined as fixed sets in `models.go`:

- **Strike kinds** (`tab_hidden`, `window_blur`, `fullscreen_exit`) — increment `ExamSession.ViolationCount`. When the count reaches the exam's `maxViolations`, the session is immediately auto-finalized (`SubmitAutoViolation`) **and** `Flagged` is set to `true` for the monitoring dashboard.
- **Logged-only kinds** (`copy`, `paste`, `cut`, `contextmenu`, `reload`) — recorded in the `violations` table for audit but don't count toward the limit.

Every violation, strike or not, is persisted with its `kind`, `strike` flag, optional free-text `meta`, and timestamp — visible to admins per-session via `GET /admin/sessions/:id`.

### 4.8 Time enforcement: grace period & sweeper

Two independent, overlapping mechanisms enforce the exam clock:

1. **Inline, on every write** (`PUT /me/answers/:id`, `POST /me/run`, and the resume path in `POST /sessions`): if `now > endsAt + ANSWER_GRACE_SEC`, the handler auto-finalizes the session (`SubmitAutoTime`) *before* rejecting the write with 409. This means a candidate who's still actively clicking around past their deadline gets cut off on their very next request.
2. **The sweeper** (`services.StartSweeper`, a 30-second ticker in `cmd/server/main.go`): independently scans for any `active` session whose `ends_at < now - grace` and force-submits it, regardless of whether the candidate makes any more requests. This is what catches a closed laptop or lost network connection — without it, a session that simply goes silent would stay `active` forever.

Both funnel through the same `services.FinalizeSession`, which does an atomic conditional update (`WHERE status = 'active'`) so it's safe if both mechanisms race — only the first one to land wins, the second gets `ErrNotActive` and is a no-op.

### 4.9 Grading

Grading is asynchronous and worker-pool-based (`services.Grader`, 4 goroutines reading off a buffered channel). `POST /me/submit` (or an auto-submit) flips the session to `grading` and enqueues it; a failed grading attempt (e.g. a transport error talking to Piston) is retried once after 30 seconds. Per question type:

- **Aptitude** — instant, exact-match: full marks if `selectedIndex == correctIndex`, else zero.
- **Coding** — re-runs the candidate's *last saved* code against **every** test case, hidden included (unlike `/me/run`, which only ever sees sample tests). Score is `marks × (sum of passed test-case weights) / (sum of all weights)`, rounded to 2 decimals — so partial credit is possible and hidden, higher-weighted tests matter more. The **first** failing compile short-circuits the rest of that question's test cases (all marked `compile_error`) rather than wasting runner time. Output comparison (`CompareOutput`/`NormalizeOutput`) trims trailing whitespace per line and surrounding blank lines before comparing, so cosmetic newline/trailing-space differences don't fail an otherwise-correct answer.
- **English** — left ungraded (`Answer.Graded = false`) for an admin to score manually via `PUT /admin/answers/:id/grade`; the session still transitions to `graded` once MCQ/coding are done — English scoring is added to `ManualScore`/`TotalScore` later without blocking the status transition.

`TotalScore = AutoScore (aptitude + coding) + ManualScore (english)`, both persisted on the `ExamSession` row and recomputed (`RecomputeManual`) every time an admin grades an English answer.

### 4.10 Monitoring & export

`GET /admin/exams/:id/sessions` is the live dashboard feed: per-candidate progress (answered count, pending-English count), violation count, flagged state, and running scores — polling-friendly for a "watch the exam happen" admin view. `GET /admin/sessions/:id` drills into one candidate (every question + their answer + full violation log). `GET /admin/exams/:id/export` streams a `text/csv` results sheet (name, email, semester, phone, set, status, submit kind, flagged, violations, scores, timestamps) for offline record-keeping.

---

## 5. Complete API reference

Base path for everything except health is **`/api/v1`**. All error responses share the shape `{"error": "<message>"}`. CORS is restricted to `CORS_ORIGINS` (comma-separated), methods `GET/POST/PUT/DELETE/OPTIONS`, headers `Origin, Content-Type, Authorization`.

### Health

| Method & path | Auth | Notes |
|---|---|---|
| `GET /healthz` | none | `200 {"ok": true, "serverNow": "<RFC3339>"}`. Not under `/api/v1`. |

### Auth

| Method & path | Auth | Body | Response |
|---|---|---|---|
| `POST /api/v1/admin/login` | none | `{email, password}` | `200 {token, email}` · `401` on bad credentials |
| `POST /api/v1/sessions` | none | `{name, email, semester, phone?}` | `201`/`200` per §4.3 · `{token, resumed, endsAt, serverNow, exam}` |

### Public

| Method & path | Auth | Response |
|---|---|---|
| `GET /api/v1/exam/active` | none | `{exam}` (nested `id/title/instructions/durationMin/maxViolations`) or `{exam: null}` |

### Candidate (`/api/v1/me/*`, session-token Bearer)

| Method & path | Notes |
|---|---|
| `GET /me` | Status snapshot: `status, studentName, setLabel, violationCount, maxViolations, submitKind, startedAt, endsAt, submittedAt, serverNow, exam`. |
| `GET /me/paper` | Sanitized question paper (no correct answers, no hidden tests) + previously saved answers. `409` if not `active`. |
| `PUT /me/answers/:questionId` | Upsert one answer. Body shape depends on question type (§4.4). `200 {savedAt}` · `404` if question isn't on this candidate's set · `409` if session isn't writable. |
| `POST /me/run` | `{questionId, language, code, mode: "samples"|"custom", stdin?}`. `200` with mode-specific shape (§4.6). `429` on rate limit, `502` if the runner itself is unreachable. |
| `POST /me/violations` | `{kind, meta?}`. `200 {violationCount, maxViolations, strike, autoSubmitted}`. `400` on unknown `kind`. |
| `POST /me/submit` | No body. `200 {status: "grading", submittedAt}`. Idempotent — safe to call again after already-submitted. |
| `POST /me/heartbeat` | `200 {status, violationCount, endsAt, serverNow}` — cheap poll target to detect a server-side auto-submit while the tab was backgrounded. |

### Admin — Questions (`/api/v1/admin/questions`, admin JWT)

| Method & path | Notes |
|---|---|
| `GET /admin/questions` | Query: `type`, `q` (title `ILIKE`), `limit` (default 100, max 500), `offset`. `200 {items, total}`. |
| `POST /admin/questions` | Create. Body = `questionPayload` (§4.1 fields). `201 <Question>`. |
| `GET /admin/questions/:id` | `200 <Question>` · `404`. |
| `PUT /admin/questions/:id` | Full replace; re-syncs `marks` onto any `SetQuestion` rows already referencing it. `200 <Question>`. |
| `DELETE /admin/questions/:id` | Cascades test cases + set links. `200`. |

### Admin — Exams (`/api/v1/admin/exams`, admin JWT)

| Method & path | Notes |
|---|---|
| `GET /admin/exams` | `200 {items}` (each with `sessionCount`), newest first. |
| `POST /admin/exams` | `{title, instructions?, durationMin?, maxViolations?}` → `draft` exam + 6 empty sets A–F. `201 <Exam>`. |
| `GET /admin/exams/:id` | Full detail, preloading `Sets → Sets.Questions → Sets.Questions.Question`. `404`. |
| `PUT /admin/exams/:id` | Partial update (non-zero fields only). `200 <Exam>`. |
| `DELETE /admin/exams/:id` | Only while `status == "draft"` (§4.2). `200 {deleted: true}` · `400` otherwise. |
| `POST /admin/exams/:id/activate` | Requires every set to have ≥1 question; auto-closes any other active exam. `200 {status: "active"}` · `400` if a set is empty. |
| `POST /admin/exams/:id/close` | No guard. `200 {status: "closed"}`. |
| `POST /admin/exams/:id/auto-distribute` | `{english, aptitude, coding, mode: "unique"|"shuffled"}` (§4.2). `200 {distributed: true}` · `400` if the bank is too small / bad mode. |
| `GET /admin/exams/:id/sessions` | Live monitoring feed (§4.10). `200 {items, serverNow}`. |
| `GET /admin/exams/:id/export` | `text/csv`, `Content-Disposition: attachment`. `404` if exam doesn't exist. |

### Admin — Sets, sessions, grading (admin JWT)

| Method & path | Notes |
|---|---|
| `PUT /admin/sets/:id/questions` | `:id` = `QuestionSet.id`. `{questionIds: [...]}`, order = position. `200 {count}` · `400` on unknown/duplicate IDs. |
| `GET /admin/sessions/:id` | Full per-candidate breakdown: session + student + set, `items` (question + answer per position), `violations`. `404`. |
| `PUT /admin/answers/:id/grade` | English-only. `{score}` (clamped ≥0, capped at question marks). Recomputes session totals. `200 {score, manualScore, totalScore}` · `400` if not an english answer or score exceeds max. |

### Complete flat route list

```
GET    /healthz
GET    /api/v1/exam/active
POST   /api/v1/sessions
POST   /api/v1/admin/login

GET    /api/v1/me
GET    /api/v1/me/paper
PUT    /api/v1/me/answers/:questionId
POST   /api/v1/me/violations
POST   /api/v1/me/run
POST   /api/v1/me/submit
POST   /api/v1/me/heartbeat

GET    /api/v1/admin/questions
POST   /api/v1/admin/questions
GET    /api/v1/admin/questions/:id
PUT    /api/v1/admin/questions/:id
DELETE /api/v1/admin/questions/:id

GET    /api/v1/admin/exams
POST   /api/v1/admin/exams
GET    /api/v1/admin/exams/:id
PUT    /api/v1/admin/exams/:id
DELETE /api/v1/admin/exams/:id
POST   /api/v1/admin/exams/:id/activate
POST   /api/v1/admin/exams/:id/close
POST   /api/v1/admin/exams/:id/auto-distribute
GET    /api/v1/admin/exams/:id/sessions
GET    /api/v1/admin/exams/:id/export

PUT    /api/v1/admin/sets/:id/questions
GET    /api/v1/admin/sessions/:id
PUT    /api/v1/admin/answers/:id/grade
```

No logout endpoint (JWT is stateless), no session-token revoke endpoint, no refresh-token endpoint, no admin "try run code" endpoint separate from the candidate one.

---

## 6. Configuration reference

Loaded from `.env` (via `godotenv`) with these fallbacks if unset (`internal/config/config.go`):

| Var | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | API listen port |
| `DATABASE_URL` | `postgres://exam:exam@localhost:5433/exam_taker?sslmode=disable` | Postgres DSN |
| `JWT_SECRET` | `dev-secret-change-me` | Admin JWT HMAC signing key — **must** be overridden outside local dev |
| `ADMIN_EMAIL` | `admin@example.com` | Seeded admin login |
| `ADMIN_PASSWORD` | `admin123` | Seeded admin login |
| `PISTON_URL` | `http://localhost:2000` | Piston base URL |
| `RUNNER` | `auto` | `auto` \| `piston` \| `docker` — see §4.5 |
| `RUNNER_CONTAINER` | `exam_taker_runner` | Container name for the docker-exec fallback |
| `CORS_ORIGINS` | `http://localhost:3000` | Comma-separated allowed origins |
| `ANSWER_GRACE_SEC` | `30` | Grace window after `endsAt` before writes are rejected / the sweeper fires (§4.8) |
| `RUN_CONCURRENCY` | `4` | Max concurrent `/me/run` executions, process-wide (§4.6) |
| `GRADER_WORKERS` | `4` | Background grading worker pool size (§1) |
| `RUN_QUEUE_TIMEOUT_SEC` | `15` | How long a run waits for a free concurrency slot before 429 (§4.6) |
| `RUN_RATE_LIMIT_SEC` | `2` | Minimum seconds between `/me/run` calls for one session (§4.6) |

---

## 7. Local development & infra

```bash
docker compose up -d                 # postgres :5433, piston :2000 (+ fallback `runner` if --profile fallback)
bash scripts/install-runtimes.sh     # one-time: installs python/gcc/java into piston's package store
cp .env.example .env
go run ./cmd/seed                    # idempotent — skips if "Campus Mock Test" already exists
go run ./cmd/server                  # :8080
```

`docker-compose.yml` runs Postgres on host port **5433** (not 5432) and Piston on **2000**, both with named volumes (`db_data`, `piston_packages`) so runtime installs and data survive container restarts. Piston runs `platform: linux/amd64` (needs Rosetta-backed emulation on Apple Silicon, not plain qemu — see README's "Code runner" section) with `privileged: true` (required for its sandbox). An `extra_hosts` entry pins `release-assets.githubusercontent.com` to a specific GitHub CDN IP — a workaround for one of GitHub's four Fastly edge IPs being unreachable from Docker's network on some hosts, which otherwise crash-loops Piston on every startup while it fetches its package index.

### Seed data (`internal/seed/seed.go`, idempotent)

Running `go run ./cmd/seed` (which also runs `EnsureAdmin`) produces:

- **Admin**: from `ADMIN_EMAIL`/`ADMIN_PASSWORD` (defaults `admin@example.com` / `admin123`).
- **13 questions**: 2 English (5 marks each), 8 Aptitude MCQs (2 marks each), 3 Coding (10 marks each, `timeLimitMs: 3000`, each with 4 test cases, 2 hidden).
- **1 exam** — "Campus Mock Test", `durationMin: 60`, `maxViolations: 3` — auto-distributed (`shuffled`, `{english:2, aptitude:8, coding:3}` = all 13 questions) across sets A–F and immediately set to `status: active`.

This means a fresh `docker compose up -d && ... && go run ./cmd/seed && go run ./cmd/server` gives you an immediately-usable, fully-populated, active exam with zero manual admin setup — useful for demoing the candidate flow, or as a baseline the Postman collection can run against (though the collection creates its own data and doesn't depend on the seed having run).

---

## 8. Production deployment

`Dockerfile` builds a static (`CGO_ENABLED=0`) multi-stage Alpine image containing both the `server` and `seed` binaries. `docker-compose.prod.yml` is a **single-compose** production stack for [Dokploy](https://dokploy.com) — Postgres + Piston + the Go API as one deployable unit, with the API on an internal `dokploy-network` for Traefik routing. Required environment (set in Dokploy's Environment tab): `POSTGRES_PASSWORD`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `JWT_SECRET`, `CORS_ORIGINS`. `RUNNER` is hardcoded to `piston` in prod (no `auto` probing, no docker-exec fallback container) since the prod host is expected to be a real Linux amd64 box where Piston's sandbox just works.

---

## 9. Testing

- **Unit tests**: `go test ./...` — currently covering `internal/services` (grading math, output normalization, set assignment/distribution logic). Handlers, middleware, and the runner clients have no dedicated Go tests; they're covered instead by the black-box collection below.
- **Full API regression (Postman/Newman)**: [`postman/exam-taker.postman_collection.json`](postman/exam-taker.postman_collection.json) — a 53-request, ~140-assertion end-to-end suite covering every route in §5 plus several behavioral edge cases that unit tests can't easily reach: candidate resume-on-duplicate-registration, 409-after-already-attempted, the 3-strike violation auto-submit cutoff, the `/me/run` rate limit, draft-only exam deletion, and CSV export content. Self-contained (default `base_url=http://localhost:8080`, admin creds matching `.env.example`) — run it against a live server with:

  ```bash
  npx newman run postman/exam-taker.postman_collection.json
  ```

---

## 10. Known limitations

- **Proctoring is deterrence-level, not device-level.** The client detects and punishes tab switches, window blur, and fullscreen exit, and the server independently enforces those limits — but nothing here can detect a second device, a phone camera, or a second person in the room. There's no webcam/screen-capture pipeline.
- **The docker-exec fallback runner** is meaningfully weaker isolation than Piston's `isolate` sandbox (container-level caps + no network + non-root, vs. proper namespace-level sandboxing) — acceptable for a college exam's threat model, not for hostile/adversarial code execution at scale.
- **Single active exam at a time, platform-wide.** There's no multi-tenancy or concurrent-exam support — activating one exam always closes any other active exam.
- **No candidate password/account system.** Identity is "whoever has the session token", handed out once via email at registration. There's no way to re-issue a lost token other than re-registering with the same email while the session is still `active`.
- **In-process background work only.** Grading workers and the auto-submit sweeper are goroutines inside the API process, not a separate durable queue — fine at this scale, but means grading throughput and sweep timing are tied to the API process's own lifecycle (mitigated for correctness, not availability, by `RequeueStuck` on restart).
