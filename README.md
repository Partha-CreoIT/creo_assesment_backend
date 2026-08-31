# Creo Assess — Backend

Go API for Creo Assess, a proctored mock-test platform: question bank (English / Aptitude MCQ / Coding), exams with six question-paper sets (A–F), random set assignment, sandboxed code execution (Python, Java, C), violation tracking with auto-submit, async grading, and results export.

Frontend lives in the sibling repo `exam_taker_fr` (Next.js).

## Stack

- Go + Gin + GORM (PostgreSQL)
- [Piston](https://github.com/engineer-man/piston) for sandboxed code execution, with a native docker-exec fallback runner
- docker-compose for Postgres + Piston (+ optional fallback runner)

## Quick start

```bash
# 1. infra
docker compose up -d                 # postgres :5433, piston :2000
bash scripts/install-runtimes.sh     # one-time: install python/gcc/java into piston

# 2. env (defaults work for local dev)
cp .env.example .env

# 3. seed demo data (admin + 13 questions + an activated exam with sets A–F)
go run ./cmd/seed

# 4. run
go run ./cmd/server                  # listens on :8080
```

Default admin: `admin@example.com` / `admin123` (change via `ADMIN_EMAIL` / `ADMIN_PASSWORD` before first run).

Tests: `go test ./...`

## Code runner

`RUNNER=auto` (default) probes Piston with a real execution at startup and falls back to the docker-exec runner if Piston can't execute.

- **Piston** (recommended): sandboxed engine used by the compose file. On Apple Silicon it needs Docker Desktop's **Rosetta** x86 emulation (Settings → General → "Use Rosetta…") — under plain qemu emulation Piston's isolate sandbox fails with `clone failed: Invalid argument`.
- **docker-exec fallback**: a native-arch container with python3/gcc/openjdk, driven via `docker exec` (non-root user, no network, memory/pids caps, kill timeouts). Start it with `docker compose --profile fallback up -d` and set `RUNNER=docker` to force it.

The compose file raises Piston's request caps (`PISTON_RUN_TIMEOUT=10000`, `PISTON_COMPILE_TIMEOUT=15000`); the client clamps question time limits to 10s.

## How it fits together

- **Admin** builds questions (marks, hints; MCQ options + correct index; coding starter code per language, syntax notes, test cases with hidden flags and weights), then an exam. Six sets A–F are created automatically and can be filled by hand or via **auto-distribute** (`unique`: different questions per set; `shuffled`: same sample, different order). Activating an exam closes any other active exam.
- **Students** register with name / email / semester (1–8) / optional phone. The server assigns the least-filled set (random tie-break), starts the clock (`duration_min`), and issues an opaque session token. The same email resumes an active session with the original clock; a submitted email is rejected.
- **Paper** is sanitized: no correct indices, no hidden test cases.
- **Answers** upsert per question (autosave); writes are rejected after `ends_at` + `ANSWER_GRACE_SEC`.
- **Runs** (`POST /me/run`) execute against visible sample tests or custom stdin, rate-limited per session (2s) and globally (4 concurrent).
- **Violations**: `tab_hidden` / `window_blur` / `fullscreen_exit` are strikes; copy/paste/right-click/reload are logged only. Reaching `max_violations` auto-submits and flags the session. A sweeper auto-submits sessions whose time expired.
- **Grading** is async: MCQ instantly; coding against *all* test cases (weighted); English left for manual grading in the admin panel, which recomputes totals. Sessions stuck in `grading` are re-queued on restart.
- **Monitoring**: per-session progress, last-seen (heartbeat), strikes, scores; CSV export.

## API surface (`/api/v1`)

Student: `GET /exam/active` · `POST /sessions` · `GET /me` · `GET /me/paper` · `PUT /me/answers/:questionId` · `POST /me/run` · `POST /me/violations` · `POST /me/heartbeat` · `POST /me/submit`

Admin (JWT via `POST /admin/login`): questions CRUD · exams CRUD + `/activate` `/close` `/auto-distribute` · `PUT /sets/:id/questions` · `GET /exams/:id/sessions` (monitor) · `GET /sessions/:id` (detail) · `PUT /answers/:id/grade` · `GET /exams/:id/export` (CSV)

## Configuration (`.env`)

| Var | Default | |
|---|---|---|
| `PORT` | `8080` | API port |
| `DATABASE_URL` | `postgres://exam:exam@localhost:5433/exam_taker?sslmode=disable` | |
| `JWT_SECRET` | dev value | set a long random string |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | `admin@example.com` / `admin123` | seeded admin |
| `PISTON_URL` | `http://localhost:2000` | |
| `RUNNER` | `auto` | `auto` \| `piston` \| `docker` |
| `RUNNER_CONTAINER` | `exam_taker_runner` | fallback container name |
| `CORS_ORIGINS` | `http://localhost:3000` | comma-separated |
| `ANSWER_GRACE_SEC` | `30` | late-save grace after time up |

## Honest limitations

- Browser proctoring is deterrence-level: the client detects and punishes tab/window/fullscreen changes, but nothing can stop a second device or a photo.
- The docker-exec fallback isolates code with container caps + no network + non-root, which is reasonable for a college exam but weaker than Piston's isolate.
