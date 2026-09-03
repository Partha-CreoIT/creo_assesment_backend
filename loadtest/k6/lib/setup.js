// Shared exam-setup boilerplate for every scenario's setup(): admin login,
// create a fresh isolated exam, build a minimal question bank, assign it to
// every set, activate. Every scenario gets its own exam this way, so runs
// are isolated and re-runnable with the same student001@test.com.. emails
// (see data.js).
//
// Sets are populated by explicit `PUT /admin/sets/:id/questions` with the
// exact question IDs just created, NOT `POST .../auto-distribute`.
// auto-distribute samples from the ENTIRE questions table (every question
// ever created by every scenario run this session, not just this exam's) —
// with `mode: shuffled` it can and does pick an unrelated pre-existing
// question instead of the one setupExam() just built, which only stayed
// invisible as long as every scenario's fixture happened to share the same
// shape. It surfaced as a real bug in 16_partial_scores.js, whose
// custom-weighted question was silently swapped for a generic one from the
// accumulated bank. Manual assignment is the only way to guarantee a
// scenario's exam actually contains the questions it just created.
import {
  adminLogin,
  createExam,
  createQuestion,
  getExam,
  updateSetQuestions,
  activateExam,
  registerCandidate,
} from './api.js';
import {
  englishQuestionPayload,
  aptitudeQuestionPayload,
  codingQuestionPayload,
  registerPayload,
} from './data.js';
import { recordMeta } from './summary.js';

// Throws on any non-2xx response — setup() failures should abort the whole
// run loudly rather than let every VU fail against a half-configured exam.
function must(res, label) {
  if (res.status < 200 || res.status >= 300) {
    throw new Error(`${label} failed: ${res.status} ${res.body}`);
  }
  return res;
}

export function adminAuth(email, password) {
  const res = must(adminLogin(email, password), 'admin login');
  return res.json('token');
}

// Creates one exam with `counts.english`/`aptitude`/`coding` distinct
// questions of each type (title-suffixed so multiple questions per type
// don't collide), auto-distributed (shuffled) across sets A-F, activated.
// Returns { adminToken, examId }.
export function setupExam({
  adminEmail = __ENV.ADMIN_EMAIL || 'admin@example.com',
  adminPassword = __ENV.ADMIN_PASSWORD || 'admin123',
  title,
  durationMin = 60,
  maxViolations = 3,
  counts = { english: 1, aptitude: 1, coding: 1 },
  codingMemoryLimitKb = 0,
  codingTimeLimitMs = 3000,
} = {}) {
  const adminToken = adminAuth(adminEmail, adminPassword);

  const examRes = must(
    createExam(adminToken, {
      title: title || `LoadTest ${Date.now()}`,
      instructions: 'Created by loadtest/k6 — see loadtest/README.md',
      durationMin,
      maxViolations,
    }),
    'create exam',
  );
  const examId = examRes.json('id');

  const builders = [
    [counts.english, englishQuestionPayload],
    [counts.aptitude, aptitudeQuestionPayload],
    [counts.coding, (n) => codingQuestionPayload(n, { memoryLimitKb: codingMemoryLimitKb, timeLimitMs: codingTimeLimitMs })],
  ];
  // English -> Aptitude -> Coding order, matching the convention documented
  // in details.md, so position-based assumptions elsewhere stay valid.
  const questionIds = [];
  for (const [count, build] of builders) {
    for (let i = 1; i <= count; i++) {
      const res = must(createQuestion(adminToken, build(i)), 'create question');
      questionIds.push(res.json('id'));
    }
  }

  if (questionIds.length > 0) {
    const examDetail = must(getExam(adminToken, examId), 'get exam detail');
    const sets = examDetail.json('sets') || [];
    for (const set of sets) {
      must(updateSetQuestions(adminToken, set.id, questionIds), `assign questions to set ${set.label}`);
    }
  }
  must(activateExam(adminToken, examId), 'activate exam');

  console.log(`EXAM_ID=${examId}`);
  recordMeta('exam_id', examId);
  return { adminToken, examId };
}

// Pre-registers N candidates (student001@test.com..) sequentially inside
// setup() — used by every scenario where registration is test-data setup,
// not the thing under measurement (paper load, autosave, heartbeat, etc.).
// Returns an array of { n, email, token } so VU code (indexed by __VU,
// 1..N) can pick its own candidate without a shared-state race — `email` is
// the reliable key for matching a candidate back to an admin-side session
// row later (candidate.n is just the registration index, NOT the DB
// Student.id, which may differ if these emails were already registered by
// an earlier run).
export function registerCandidates(n) {
  const candidates = [];
  for (let i = 1; i <= n; i++) {
    const payload = registerPayload(i);
    const res = must(registerCandidate(payload), `register candidate ${i}`);
    candidates.push({ n: i, email: payload.email, token: res.json('token') });
  }
  return candidates;
}
