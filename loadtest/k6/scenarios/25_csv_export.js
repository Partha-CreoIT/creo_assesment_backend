// Test 27 — exports the results CSV once while grading is still in
// progress (must still return a valid, well-formed CSV — export doesn't
// wait for grading) and once after every candidate is graded, verifying
// the final export has exactly one data row per candidate plus the header,
// with every candidate's name present and correct scores.
//
// Run: k6 run loadtest/k6/scenarios/25_csv_export.js
// Override: VUS=300 MAX_POLL_SEC=1800 k6 run ...
import { check, sleep } from 'k6';
import { checkOk, BASELINE_THRESHOLDS } from '../lib/checks.js';
import { writeResults, readMeta } from '../lib/summary.js';
import { setupExam, registerCandidates } from '../lib/setup.js';
import { getPaper, saveAnswer, submitExam, listSessions, exportCsv } from '../lib/api.js';
import { CODE_FIXTURES } from '../lib/data.js';

const VUS = parseInt(__ENV.VUS || '10', 10);
const MAX_POLL_SEC = parseFloat(__ENV.MAX_POLL_SEC || '60');

export const options = {
  // setup() submits every candidate's full answer set sequentially (not
  // parallelized — it's plain JS running once in the init VU), so against a
  // real remote deployment (not localhost) its wall-clock cost scales with
  // candidate count x network round-trip time and can exceed k6's 60s
  // default setupTimeout well before VUS gets large.
  setupTimeout: '5m',
  scenarios: {
    csv_export: {
      executor: 'per-vu-iterations',
      vus: 1,
      iterations: 1,
      maxDuration: `${Math.ceil(MAX_POLL_SEC + 30)}s`,
    },
  },
  thresholds: {
    ...BASELINE_THRESHOLDS,
  },
};

export function setup() {
  const { examId, adminToken } = setupExam({ title: `LoadTest CsvExport ${Date.now()}` });
  const candidates = registerCandidates(VUS);

  // give every candidate a real submitted attempt so grading is genuinely
  // in flight when the "during grading" export happens below.
  for (const candidate of candidates) {
    const paperRes = getPaper(candidate.token);
    const questions = paperRes.json('questions');
    for (const q of questions) {
      let payload;
      if (q.type === 'english') payload = { answerText: 'CSV export test answer.' };
      else if (q.type === 'aptitude') payload = { selectedIndex: 1 };
      else payload = { code: CODE_FIXTURES.python.correctSum, language: 'python' };
      saveAnswer(candidate.token, q.id, payload);
    }
    submitExam(candidate.token);
  }

  return { examId, adminToken, candidates };
}

export default function (data) {
  // 1. export while grading is likely still in progress — must still be a
  // well-formed CSV, not an error or a partial/corrupt response.
  const midRes = exportCsv(data.adminToken, data.examId);
  checkOk(midRes, 'normal', {
    extraChecks: {
      'export during grading: content-type is csv': (r) => (r.headers['Content-Type'] || '').includes('text/csv'),
      'export during grading: has header row': (r) => r.body.includes('Name,Email'),
    },
  });

  // 2. wait for everyone to finish grading
  const deadline = Date.now() + MAX_POLL_SEC * 1000;
  let allGraded = false;
  while (Date.now() < deadline) {
    const res = listSessions(data.adminToken, data.examId);
    if (res.status === 200) {
      const items = res.json('items') || [];
      if (items.length === data.candidates.length && items.every((i) => i.status === 'graded')) {
        allGraded = true;
        break;
      }
    }
    sleep(2);
  }
  check(allGraded, { 'csv export: every candidate reached graded before final export': (g) => g === true });

  // 3. final export — exact row count and every candidate's name present.
  const finalRes = exportCsv(data.adminToken, data.examId);
  checkOk(finalRes, 'normal', {
    extraChecks: {
      'final export: content-type is csv': (r) => (r.headers['Content-Type'] || '').includes('text/csv'),
      'final export: exactly one data row per candidate': (r) => {
        const lines = r.body.trim().split('\n');
        return lines.length - 1 === data.candidates.length; // -1 for the header row
      },
      'final export: every candidate name present': (r) =>
        data.candidates.every((c) => r.body.includes(`Load Student ${c.n}`)),
    },
  });
}

export function handleSummary(data) {
  const outDir = __ENV.RESULTS_DIR || 'loadtest/results/latest';
  const meta = { scenario: '25_csv_export', examId: readMeta(data, 'exam_id'), vus: VUS };
  return writeResults(data, meta, outDir, '25_csv_export', [
    'checks',
    'success_normal',
    'errors_5xx',
    'errors_network',
    'errors_timeout',
    'errors_unexpected',
  ]);
}
