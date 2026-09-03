// The expected-vs-failure classifier used by every scenario, so "the app
// stayed healthy under load" is judged consistently rather than by
// convention. See details.md §4.6 and the plan's B.2 pass/fail table.
//
// Only use classify()/checkOk() for "happy path" requests. A scenario that
// deliberately expects a 400/404/409 (e.g. duplicate registration, invalid
// input) should assert that status directly with k6's check(), not route it
// through this classifier — those are correct-and-expected outcomes for
// that specific test, not "normal traffic that must succeed."
import { check } from 'k6';
import { Rate } from 'k6/metrics';

// Intentional throttling on /me/run — tracked separately, never counted as
// an error. A high rate here is expected and fine; it is not a threshold.
export const throttledRun = new Rate('throttled_run');

// Failure buckets — see the plan's "expected vs. failure" table.
export const errors5xx = new Rate('errors_5xx');
export const errorsNetwork = new Rate('errors_network');
export const errorsTimeout = new Rate('errors_timeout');
export const errorsUnexpected = new Rate('errors_unexpected'); // any other non-2xx on a happy-path call

// Per-endpoint-category success rates, matching the B.2 targets exactly.
export const successRegistration = new Rate('success_registration');
export const successPaper = new Rate('success_paper');
export const successAnswers = new Rate('success_answers');
export const successHeartbeat = new Rate('success_heartbeat');
export const successNormal = new Rate('success_normal');

const CATEGORY_METRIC = {
  registration: successRegistration,
  paper: successPaper,
  answers: successAnswers,
  heartbeat: successHeartbeat,
};

function categoryMetric(category) {
  return CATEGORY_METRIC[category] || successNormal;
}

// classify() records metrics for one response and returns one of:
//   'ok'                — 2xx
//   'expected_throttle'  — 429 on /me/run specifically (isRunEndpoint: true)
//   'failure'            — everything else: 5xx, network/connection errors,
//                          timeouts, or any other unexpected non-2xx
export function classify(res, category, { isRunEndpoint = false } = {}) {
  const status = res.status;

  // k6 reports status 0 when a request never got an HTTP response at all
  // (connection refused/reset, DNS failure, or it hit the client timeout).
  if (status === 0) {
    const msg = (res.error || '').toLowerCase();
    if (res.error_code === 1050 || msg.includes('timeout')) {
      errorsTimeout.add(1);
    } else {
      errorsNetwork.add(1);
    }
    categoryMetric(category).add(0);
    return 'failure';
  }

  if (status === 429 && isRunEndpoint) {
    throttledRun.add(1);
    return 'expected_throttle';
  }

  if (status >= 200 && status < 300) {
    categoryMetric(category).add(1);
    return 'ok';
  }

  // Any other status on a happy-path call is a failure of this criterion,
  // even a "well-formed" 4xx — see the file header.
  if (status >= 500) errors5xx.add(1);
  errorsUnexpected.add(1);
  categoryMetric(category).add(0);
  return 'failure';
}

// checkOk() combines classify() with a k6 check() so scenario files get a
// pass/fail line in the terminal output for free. `extraChecks` follows
// k6's normal check() shape (name -> predicate(res)).
export function checkOk(res, category, opts = {}) {
  const { isRunEndpoint = false, extraChecks = {} } = opts;
  const outcome = classify(res, category, { isRunEndpoint });
  return check(res, {
    [`${category}: healthy (2xx or expected 429)`]: () =>
      outcome === 'ok' || outcome === 'expected_throttle',
    ...extraChecks,
  });
}

// Ready-made k6 `thresholds` entries matching the plan's B.2 table. Spread
// this into a scenario's `options.thresholds` and add scenario-specific
// entries (e.g. http_req_duration per tagged endpoint) alongside it.
export const BASELINE_THRESHOLDS = {
  success_registration: ['rate>=0.995'],
  success_paper: ['rate>=0.999'],
  success_answers: ['rate>=0.999'],
  success_heartbeat: ['rate>=0.999'],
  success_normal: ['rate>=0.99'],
  errors_5xx: ['rate<0.001'],
  errors_network: ['rate<0.001'],
  errors_timeout: ['rate<0.001'],
};
