// Thin HTTP wrappers, one per backend endpoint. No assertions live here —
// callers classify outcomes via lib/checks.js. Every function returns the
// raw k6 http response so callers can inspect status/body/timing as needed.
import http from 'k6/http';

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

const JSON_HEADERS = { 'Content-Type': 'application/json' };

function authHeaders(token) {
  return { Authorization: `Bearer ${token}`, ...JSON_HEADERS };
}

function tagged(name) {
  return { tags: { name } };
}

// ---- Health -----------------------------------------------------------

export function healthz() {
  return http.get(`${BASE_URL}/healthz`, tagged('healthz'));
}

// ---- Admin auth ---------------------------------------------------------

export function adminLogin(email, password) {
  return http.post(
    `${BASE_URL}/api/v1/admin/login`,
    JSON.stringify({ email, password }),
    { headers: JSON_HEADERS, ...tagged('admin_login') },
  );
}

// ---- Admin: questions -----------------------------------------------------

export function createQuestion(adminToken, payload) {
  return http.post(`${BASE_URL}/api/v1/admin/questions`, JSON.stringify(payload), {
    headers: authHeaders(adminToken),
    ...tagged('admin_create_question'),
  });
}

export function listQuestions(adminToken, params = '') {
  return http.get(`${BASE_URL}/api/v1/admin/questions${params}`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_list_questions'),
  });
}

export function getQuestion(adminToken, questionId) {
  return http.get(`${BASE_URL}/api/v1/admin/questions/${questionId}`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_get_question'),
  });
}

export function updateQuestion(adminToken, questionId, payload) {
  return http.put(`${BASE_URL}/api/v1/admin/questions/${questionId}`, JSON.stringify(payload), {
    headers: authHeaders(adminToken),
    ...tagged('admin_update_question'),
  });
}

export function deleteQuestion(adminToken, questionId) {
  return http.del(`${BASE_URL}/api/v1/admin/questions/${questionId}`, null, {
    headers: authHeaders(adminToken),
    ...tagged('admin_delete_question'),
  });
}

// ---- Admin: exams -----------------------------------------------------

export function createExam(adminToken, payload) {
  return http.post(`${BASE_URL}/api/v1/admin/exams`, JSON.stringify(payload), {
    headers: authHeaders(adminToken),
    ...tagged('admin_create_exam'),
  });
}

export function getExam(adminToken, examId) {
  return http.get(`${BASE_URL}/api/v1/admin/exams/${examId}`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_get_exam'),
  });
}

export function updateSetQuestions(adminToken, setId, questionIds) {
  return http.put(
    `${BASE_URL}/api/v1/admin/sets/${setId}/questions`,
    JSON.stringify({ questionIds }),
    { headers: authHeaders(adminToken), ...tagged('admin_update_set') },
  );
}

export function autoDistribute(adminToken, examId, payload) {
  return http.post(
    `${BASE_URL}/api/v1/admin/exams/${examId}/auto-distribute`,
    JSON.stringify(payload),
    { headers: authHeaders(adminToken), ...tagged('admin_auto_distribute') },
  );
}

export function activateExam(adminToken, examId) {
  return http.post(`${BASE_URL}/api/v1/admin/exams/${examId}/activate`, null, {
    headers: authHeaders(adminToken),
    ...tagged('admin_activate_exam'),
  });
}

export function closeExam(adminToken, examId) {
  return http.post(`${BASE_URL}/api/v1/admin/exams/${examId}/close`, null, {
    headers: authHeaders(adminToken),
    ...tagged('admin_close_exam'),
  });
}

export function deleteExam(adminToken, examId) {
  return http.del(`${BASE_URL}/api/v1/admin/exams/${examId}`, null, {
    headers: authHeaders(adminToken),
    ...tagged('admin_delete_exam'),
  });
}

export function listSessions(adminToken, examId) {
  return http.get(`${BASE_URL}/api/v1/admin/exams/${examId}/sessions`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_list_sessions'),
  });
}

export function exportCsv(adminToken, examId) {
  return http.get(`${BASE_URL}/api/v1/admin/exams/${examId}/export`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_export_csv'),
  });
}

export function getSessionDetail(adminToken, sessionId) {
  return http.get(`${BASE_URL}/api/v1/admin/sessions/${sessionId}`, {
    headers: authHeaders(adminToken),
    ...tagged('admin_session_detail'),
  });
}

export function gradeAnswer(adminToken, answerId, score) {
  return http.put(
    `${BASE_URL}/api/v1/admin/answers/${answerId}/grade`,
    JSON.stringify({ score }),
    { headers: authHeaders(adminToken), ...tagged('admin_grade_answer') },
  );
}

// ---- Public -----------------------------------------------------------

export function getActiveExam() {
  return http.get(`${BASE_URL}/api/v1/exam/active`, tagged('exam_active'));
}

// ---- Candidate ----------------------------------------------------------

export function registerCandidate(payload) {
  return http.post(`${BASE_URL}/api/v1/sessions`, JSON.stringify(payload), {
    headers: JSON_HEADERS,
    ...tagged('register'),
  });
}

export function getMe(token) {
  return http.get(`${BASE_URL}/api/v1/me`, { headers: authHeaders(token), ...tagged('me') });
}

export function getPaper(token) {
  return http.get(`${BASE_URL}/api/v1/me/paper`, {
    headers: authHeaders(token),
    ...tagged('paper'),
  });
}

export function saveAnswer(token, questionId, payload) {
  return http.put(
    `${BASE_URL}/api/v1/me/answers/${questionId}`,
    JSON.stringify(payload),
    { headers: authHeaders(token), ...tagged('answer') },
  );
}

export function runCode(token, payload) {
  return http.post(`${BASE_URL}/api/v1/me/run`, JSON.stringify(payload), {
    headers: authHeaders(token),
    ...tagged('run'),
  });
}

export function reportViolation(token, payload) {
  return http.post(`${BASE_URL}/api/v1/me/violations`, JSON.stringify(payload), {
    headers: authHeaders(token),
    ...tagged('violation'),
  });
}

export function heartbeat(token) {
  return http.post(`${BASE_URL}/api/v1/me/heartbeat`, null, {
    headers: authHeaders(token),
    ...tagged('heartbeat'),
  });
}

export function submitExam(token) {
  return http.post(`${BASE_URL}/api/v1/me/submit`, null, {
    headers: authHeaders(token),
    ...tagged('submit'),
  });
}
