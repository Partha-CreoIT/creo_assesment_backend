// Test 3 — N candidates autosave realistically: answer each question type,
// change an answer, clear an answer, save repeatedly, with think-time
// between actions. This is a PACED scenario (not a synchronized burst — see
// loadtest/README.md's VU convention): each candidate's own actions are
// spaced out, and a small random jitter before the first action keeps even
// VU start times from landing in the exact same tick.
//
// Run: k6 run loadtest/k6/scenarios/03_autosave.js
// Override candidate count: VUS=300 k6 run ...  (default 10, for smoke tests)
import { sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);

function thinkTime() {
  sleep(0.2 + Math.random() * 0.8); // 0.2-1.0s between a candidate's own actions
}

export const options = {
  scenarios: {
    autosave_paced: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '3m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest Autosave ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

function saveOk(token, questionId, payload, label) {
  const res = saveAnswer(token, questionId, payload);
  return checkOk(res, 'answers', {
    extraChecks: { [`answers: ${label} savedAt present`]: (r) => !!r.json('savedAt') },
  });
}

export default function (data) {
  sleep(Math.random() * 2); // spread out even the first action — see file header
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const questions = paperRes.json('questions');
  const english = questions.find((q) => q.type === 'english');
  const aptitude = questions.find((q) => q.type === 'aptitude');
  const coding = questions.find((q) => q.type === 'coding');

  // 1. answer each type
  saveOk(token, english.id, { answerText: 'The passage argues that costs are the primary driver.' }, 'english initial');
  thinkTime();
  saveOk(token, aptitude.id, { selectedIndex: 1 }, 'aptitude initial');
  thinkTime();
  saveOk(token, coding.id, { code: 'a, b = map(int, input().split())\nprint(a + b)', language: 'python' }, 'coding initial');
  thinkTime();

  // 2. change an answer
  saveOk(
    token,
    english.id,
    { answerText: 'On reflection, the passage actually emphasizes accessibility over cost.' },
    'english changed',
  );
  thinkTime();

  // 3. clear an answer (selectedIndex: null means "unanswered", per details.md)
  saveOk(token, aptitude.id, { selectedIndex: null }, 'aptitude cleared');
  thinkTime();

  // 4. re-answer after clearing, then repeated autosave on coding (simulates
  // a candidate iterating on their solution)
  saveOk(token, aptitude.id, { selectedIndex: 2 }, 'aptitude re-answered');
  thinkTime();
  saveOk(token, coding.id, { code: 'a, b = map(int, input().split())\nprint(a+b)  # tidied', language: 'python' }, 'coding resave 1');
  thinkTime();
  saveOk(token, coding.id, { code: 'a, b = map(int, input().split())\nprint(a + b)\n', language: 'python' }, 'coding resave 2');
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '03_autosave', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '03_autosave', [
    'checks',
    'success_answers',
    'success_paper',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
