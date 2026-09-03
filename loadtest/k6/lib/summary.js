// Shared result-writing for every scenario's handleSummary(). k6's raw Rate
// metric JSON uses generic bucket names ("passes"/"fails" = count of
// true/false additions, not "test passed/failed") which reads confusingly
// for a metric like http_req_failed (true == a failure). This renders each
// named metric as a clear "name: NN.NN% (x/total)" line instead.
import { Gauge } from 'k6/metrics';

// Carries a scalar value (e.g. the exam ID a setup() call created) from
// setup() into handleSummary(). Plain JS module-level state does NOT work
// for this — k6 gives setup()/the VU code/handleSummary() each a fresh
// module instantiation, so a `let` reassigned in setup() reads back as its
// original value in handleSummary(). Metrics are the one thing k6 properly
// threads through into the final `data` object handleSummary receives, so
// numeric setup() results are recorded as a Gauge and read back from there.
// (Values derived purely from __ENV/consts need no such trick — every
// context recomputes them identically from the same input.)
//
// k6 requires metric objects to be created in the init context (module top
// level), not dynamically at runtime — so every meta key scenarios might
// carry has to be pre-declared here rather than created on first use. Add a
// new key when a scenario needs to carry a new kind of setup() value.
const META_GAUGES = {
  exam_id: new Gauge('meta_exam_id'),
  exam_b_id: new Gauge('meta_exam_b_id'), // scenarios that create a 2nd exam (e.g. 28_multi_exam_activation.js)
  throwaway_exam_id: new Gauge('meta_throwaway_exam_id'),
  admin_session_id: new Gauge('meta_admin_session_id'),
};

export function recordMeta(name, value) {
  const g = META_GAUGES[name];
  if (!g) throw new Error(`recordMeta: unknown meta key "${name}" — add it to META_GAUGES in lib/summary.js`);
  g.add(value);
}
export function readMeta(data, name) {
  const m = data.metrics[`meta_${name}`];
  return m ? m.values.value : undefined;
}

export function renderMetric(data, name) {
  const m = data.metrics[name];
  if (!m) return `${name}: (no data)`;
  const v = m.values;
  if (typeof v.rate === 'number') {
    const total = (v.passes || 0) + (v.fails || 0);
    return `${name}: ${(v.rate * 100).toFixed(2)}% (${v.passes}/${total})`;
  }
  return `${name}: ${JSON.stringify(v)}`;
}

// Writes the full k6 summary + a small run-meta file (whatever setup()
// captured — exam ID, candidate count, etc.) under outDir, and prints a
// compact labeled block of the metrics that matter for this scenario.
export function writeResults(data, meta, outDir, scenarioName, metricNames) {
  const lines = metricNames.map((m) => renderMetric(data, m));
  return {
    [`${outDir}/${scenarioName}.summary.json`]: JSON.stringify(data, null, 2),
    [`${outDir}/${scenarioName}.meta.json`]: JSON.stringify(meta, null, 2),
    stdout: lines.join('\n') + '\n',
  };
}
