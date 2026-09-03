// verify runs SQL-level invariant checks against a load-tested exam that a
// load generator can't see from HTTP responses alone: exact counts, no
// duplicate sessions, set-distribution skew, cross-candidate data leakage,
// stuck grading, and score correctness. See loadtest/README.md.
//
// Usage:
//
//	go run ./loadtest/verify --exam-id=5 --check=all
//	go run ./loadtest/verify --exam-id=5 --check=registrations --expected-count=300
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"gorm.io/gorm"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/database"
	"exam_taker_bc/internal/models"
)

type checkResult struct {
	Name   string
	Pass   bool
	Detail string
}

// checkFunc has access to the raw flag set (via *args) so individual checks
// can read their own optional flags (e.g. --expected-count) without a
// combinatorial explosion of check-specific main() plumbing.
type checkFunc func(db *gorm.DB, examID uint, a *args) checkResult

type args struct {
	examID        uint
	expectedCount uint
}

// registry grows one entry per phase as the corresponding scenarios land;
// Phase 1 ships the registration-flow checks (Test 1). Keep names stable —
// they're referenced from loadtest/README.md and Makefile targets.
var registry = map[string]checkFunc{
	"registrations": checkRegistrations,
	"sets":          checkSetDistribution,
	"answers":       checkAnswers,
	"violations":    checkViolations,
	"grading":       checkGrading,
	"time_expiry":   checkTimeExpiry,
}

func main() {
	examID := flag.Uint("exam-id", 0, "exam ID to verify (required)")
	checkName := flag.String("check", "all", "check to run, or 'all'")
	expectedCount := flag.Uint("expected-count", 0, "expected candidate/session count (0 = skip the exact-count assertion)")
	flag.Parse()

	if *examID == 0 {
		fmt.Fprintln(os.Stderr, "verify: --exam-id is required")
		os.Exit(2)
	}

	names, err := resolveChecks(*checkName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(2)
	}

	cfg := config.Load()
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify: connect db: %v\n", err)
		os.Exit(1)
	}

	a := &args{examID: *examID, expectedCount: *expectedCount}
	allPass := true
	for _, name := range names {
		res := registry[name](db, *examID, a)
		status := "PASS"
		if !res.Pass {
			status = "FAIL"
			allPass = false
		}
		fmt.Printf("[%s] %-16s %s\n", status, res.Name, res.Detail)
	}

	if !allPass {
		os.Exit(1)
	}
}

func resolveChecks(name string) ([]string, error) {
	if name == "all" {
		names := make([]string, 0, len(registry))
		for n := range registry {
			names = append(names, n)
		}
		sort.Strings(names)
		return names, nil
	}
	if _, ok := registry[name]; !ok {
		known := make([]string, 0, len(registry))
		for n := range registry {
			known = append(known, n)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown check %q (known: %v, or 'all')", name, known)
	}
	return []string{name}, nil
}

// checkRegistrations verifies Test 1's core invariants: no duplicate
// (exam_id, student_id) sessions (defense in depth — the DB already enforces
// this via a unique index, this proves it held), every session has a set
// assigned, and — if --expected-count was given — the exact session count.
//
// Deliberately NOT asserted here: total Student row count. Student.email is
// globally unique and reused verbatim across runs (see data.js), so
// find-or-create means re-running against a fresh exam does not create new
// Student rows for already-known emails — only session count is meaningful
// across repeated runs.
func checkRegistrations(db *gorm.DB, examID uint, a *args) checkResult {
	var totalSessions int64
	if err := db.Model(&models.ExamSession{}).Where("exam_id = ?", examID).Count(&totalSessions).Error; err != nil {
		return checkResult{"registrations", false, fmt.Sprintf("query failed: %v", err)}
	}

	var dupeCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT student_id FROM exam_sessions WHERE exam_id = ?
			GROUP BY student_id HAVING COUNT(*) > 1
		) d`, examID).Scan(&dupeCount).Error; err != nil {
		return checkResult{"registrations", false, fmt.Sprintf("dup query failed: %v", err)}
	}

	var unsetCount int64
	if err := db.Model(&models.ExamSession{}).
		Where("exam_id = ? AND (set_id = 0 OR set_id IS NULL)", examID).
		Count(&unsetCount).Error; err != nil {
		return checkResult{"registrations", false, fmt.Sprintf("unset-set query failed: %v", err)}
	}

	detail := fmt.Sprintf("sessions=%d duplicate_students=%d sessions_without_set=%d", totalSessions, dupeCount, unsetCount)
	pass := dupeCount == 0 && unsetCount == 0
	if a.expectedCount > 0 {
		detail += fmt.Sprintf(" expected=%d", a.expectedCount)
		pass = pass && totalSessions == int64(a.expectedCount)
	}
	return checkResult{"registrations", pass, detail}
}

// checkSetDistribution reports how sessions spread across sets A-F and
// fails only on a clearly broken distribution (AssignSet is least-filled
// with random tie-break, so small variance — e.g. 48/51/50/52/49/50 — is
// expected and fine; something like 91/12/48/53/49/47 is not).
func checkSetDistribution(db *gorm.DB, examID uint, a *args) checkResult {
	type row struct {
		Label string
		N     int64
	}
	var rows []row
	if err := db.Raw(`
		SELECT qs.label AS label, COUNT(es.id) AS n
		FROM question_sets qs
		LEFT JOIN exam_sessions es ON es.set_id = qs.id
		WHERE qs.exam_id = ?
		GROUP BY qs.label ORDER BY qs.label`, examID).Scan(&rows).Error; err != nil {
		return checkResult{"sets", false, fmt.Sprintf("query failed: %v", err)}
	}
	if len(rows) == 0 {
		return checkResult{"sets", false, "exam has no question sets"}
	}

	var total int64
	minN, maxN := rows[0].N, rows[0].N
	counts := make([]string, 0, len(rows))
	for _, r := range rows {
		total += r.N
		if r.N < minN {
			minN = r.N
		}
		if r.N > maxN {
			maxN = r.N
		}
		counts = append(counts, fmt.Sprintf("%s=%d", r.Label, r.N))
	}
	avg := float64(total) / float64(len(rows))

	// Flag a broken distribution (e.g. AssignSet's tie-break biased toward
	// one set) rather than enforcing a tight statistical bound. Sessions land
	// roughly Poisson-distributed across sets under least-filled assignment,
	// so natural spread scales with sqrt(avg) — a fixed relative-spread
	// threshold would false-flag small-N runs (e.g. a 10-candidate smoke
	// test) where one set having 3 and another 1 is completely normal. Allow
	// up to max(3, 2.5*sqrt(avg)) deviation from the mean in either
	// direction; this comfortably passes even distributions like
	// 48/51/50/52/49/50 while still catching a badly skewed one like
	// 91/12/48/53/49/47.
	maxDeviation := math.Max(3, 2.5*math.Sqrt(avg))
	pass := float64(maxN)-avg <= maxDeviation && avg-float64(minN) <= maxDeviation
	return checkResult{"sets", pass, fmt.Sprintf("%v (min=%d max=%d avg=%.1f allowed_dev=%.1f)", counts, minN, maxN, avg, maxDeviation)}
}

// checkAnswers verifies Test 3's core invariants: the exact expected answer
// row count (sessions x questions-per-set — every set in this harness's
// exams has the same question count, so any set's count works as ground
// truth without needing an external --expected-count) and no duplicate
// (session_id, question_id) rows, which would indicate the autosave upsert
// broke and/or one candidate's saves clobbered another's.
func checkAnswers(db *gorm.DB, examID uint, a *args) checkResult {
	var sessionCount int64
	if err := db.Model(&models.ExamSession{}).Where("exam_id = ?", examID).Count(&sessionCount).Error; err != nil {
		return checkResult{"answers", false, fmt.Sprintf("session count query failed: %v", err)}
	}

	var questionsPerSet int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM set_questions sq
		JOIN question_sets qs ON qs.id = sq.set_id
		WHERE qs.exam_id = ?
		GROUP BY sq.set_id LIMIT 1`, examID).Scan(&questionsPerSet).Error; err != nil {
		return checkResult{"answers", false, fmt.Sprintf("questions-per-set query failed: %v", err)}
	}
	expected := sessionCount * questionsPerSet

	var actual int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM answers a
		JOIN exam_sessions es ON es.id = a.session_id
		WHERE es.exam_id = ?`, examID).Scan(&actual).Error; err != nil {
		return checkResult{"answers", false, fmt.Sprintf("answer count query failed: %v", err)}
	}

	var dupeCount int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM (
			SELECT a.session_id, a.question_id FROM answers a
			JOIN exam_sessions es ON es.id = a.session_id
			WHERE es.exam_id = ?
			GROUP BY a.session_id, a.question_id HAVING COUNT(*) > 1
		) d`, examID).Scan(&dupeCount).Error; err != nil {
		return checkResult{"answers", false, fmt.Sprintf("dupe query failed: %v", err)}
	}

	pass := actual == expected && dupeCount == 0
	detail := fmt.Sprintf("answers=%d expected=%d (sessions=%d x questions_per_set=%d) duplicate_rows=%d",
		actual, expected, sessionCount, questionsPerSet, dupeCount)
	return checkResult{"answers", pass, detail}
}

// checkViolations verifies Test 6's invariants directly against the DB
// (independent of what the API responses claimed): every strike-kind
// violation is flagged strike=true and every other kind strike=false
// (models.StrikeKinds / LoggedKinds), and every session that reached
// max_violations is both flagged and no longer active.
func checkViolations(db *gorm.DB, examID uint, a *args) checkResult {
	var badStrikeFlags int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM violations v
		JOIN exam_sessions es ON es.id = v.session_id
		WHERE es.exam_id = ?
		AND ((v.kind IN ('tab_hidden','window_blur','fullscreen_exit') AND v.strike = false)
		  OR (v.kind NOT IN ('tab_hidden','window_blur','fullscreen_exit') AND v.strike = true))`,
		examID).Scan(&badStrikeFlags).Error; err != nil {
		return checkResult{"violations", false, fmt.Sprintf("strike-flag query failed: %v", err)}
	}

	var badAutoSubmit int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM exam_sessions es
		JOIN exams e ON e.id = es.exam_id
		WHERE es.exam_id = ? AND es.violation_count >= e.max_violations
		AND (es.flagged = false OR es.status = 'active')`,
		examID).Scan(&badAutoSubmit).Error; err != nil {
		return checkResult{"violations", false, fmt.Sprintf("auto-submit query failed: %v", err)}
	}

	pass := badStrikeFlags == 0 && badAutoSubmit == 0
	detail := fmt.Sprintf("bad_strike_flags=%d bad_auto_submit_states=%d", badStrikeFlags, badAutoSubmit)
	return checkResult{"violations", pass, detail}
}

// checkGrading reports how many of this exam's sessions are active/grading/
// graded and fails only if any are still stuck in `grading` — run this
// after waiting (the grading queue may legitimately take a while under
// pressure; see loadtest/README.md's two-sided grading measurement), not
// immediately after a submit storm.
func checkGrading(db *gorm.DB, examID uint, a *args) checkResult {
	var active, grading, graded int64
	db.Model(&models.ExamSession{}).Where("exam_id = ? AND status = ?", examID, models.SessionActive).Count(&active)
	db.Model(&models.ExamSession{}).Where("exam_id = ? AND status = ?", examID, models.SessionGrading).Count(&grading)
	db.Model(&models.ExamSession{}).Where("exam_id = ? AND status = ?", examID, models.SessionGraded).Count(&graded)

	pass := grading == 0
	detail := fmt.Sprintf("active=%d grading=%d graded=%d", active, grading, graded)
	return checkResult{"grading", pass, detail}
}

// checkTimeExpiry catches any session still `active` well past its grace
// deadline (endsAt + ANSWER_GRACE_SEC) — the sweeper (services.StartSweeper)
// ticks every 30s, so a 45s buffer avoids flagging a session still inside
// its legitimate grace window. Generically useful for any exam, not just a
// dedicated time-expiry test.
func checkTimeExpiry(db *gorm.DB, examID uint, a *args) checkResult {
	var overdueActive int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM exam_sessions
		WHERE exam_id = ? AND status = 'active' AND ends_at < ?`,
		examID, time.Now().UTC().Add(-45*time.Second)).Scan(&overdueActive).Error; err != nil {
		return checkResult{"time_expiry", false, fmt.Sprintf("query failed: %v", err)}
	}
	pass := overdueActive == 0
	return checkResult{"time_expiry", pass, fmt.Sprintf("sessions_overdue_still_active=%d", overdueActive)}
}
