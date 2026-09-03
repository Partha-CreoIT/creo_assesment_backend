// Test 4 — N candidates each fire BURST_SIZE rapid, back-to-back saves on
// the same (coding) question with NO think-time — a synchronized burst
// stressing DB connection contention and GORM upsert behavior under
// concurrency, distinct from Test 3's realistic paced autosave.
//
// Run: VUS=300 BURST_SIZE=5 k6 run loadtest/k6/scenarios/04_autosave_burst.js
// then again with BURST_SIZE=10 per the plan (300 x 5 = 1500 reqs, then x10 = 3000).
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer } from '../lib/api.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const BURST_SIZE = parseInt(__ENV.BURST_SIZE || '5', 10);

export const options = {
  scenarios: {
    autosave_burst: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: 1,
      maxDuration: '2m',
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId } = setupExam({ title: `LoadTest AutosaveBurst ${Date.now()}` });
  const candidates = registerCandidates(VUS);
  return { examId, candidates };
}

export default function (data) {
  const candidate = data.candidates[__VU - 1];
  const token = candidate.token;

  const paperRes = getPaper(token);
  checkOk(paperRes, 'paper');
  const questions = paperRes.json('questions');
  const english = questions.find((q) => q.type === 'english');
  const aptitude = questions.find((q) => q.type === 'aptitude');
  const coding = questions.find((q) => q.type === 'coding');

  // One save each for english/aptitude so the exam has a normal 3-answer
  // shape (matches the verify tool's answers check); the burst itself
  // targets the coding question, upserted BURST_SIZE times with no delay.
  checkOk(saveAnswer(token, english.id, { answerText: 'Quick answer before the burst.' }), 'answers');
  checkOk(saveAnswer(token, aptitude.id, { selectedIndex: 0 }), 'answers');

  for (let i = 0; i < BURST_SIZE; i++) {
    const res = saveAnswer(token, coding.id, {
      code: `a, b = map(int, input().split())\nprint(a + b)  # rev ${i}`,
      language: 'python',
    });
    checkOk(res, 'answers');
  }
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '04_autosave_burst', examId: readMeta(data, 'exam_id'), vus: VUS, burstSize: BURST_SIZE };
  return writeResults(data, meta, outDir, '04_autosave_burst', [
    'checks',
    'success_answers',
    'success_paper',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
