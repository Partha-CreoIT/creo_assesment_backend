package services

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
	"exam_taker_bc/internal/runner"
)

const maxStoredOutput = 2000

// Grader grades submitted sessions in the background: MCQ instantly,
// coding answers against every test case (hidden included) via Piston.
// English answers are left for manual grading by the admin.
type Grader struct {
	db     *gorm.DB
	runner runner.Runner
	queue  chan uint
}

func NewGrader(db *gorm.DB, r runner.Runner) *Grader {
	return &Grader{db: db, runner: r, queue: make(chan uint, 1024)}
}

func (g *Grader) Start(workers int) {
	if workers <= 0 {
		workers = 2
	}
	for i := 0; i < workers; i++ {
		go g.work()
	}
}

func (g *Grader) Enqueue(sessionID uint) {
	select {
	case g.queue <- sessionID:
	default:
		log.Printf("grading queue full, dropping session %d (will be picked up on restart)", sessionID)
	}
}

// RequeueStuck re-enqueues sessions left in 'grading' (e.g. after a restart).
func (g *Grader) RequeueStuck() {
	var ids []uint
	if err := g.db.Model(&models.ExamSession{}).
		Where("status = ?", models.SessionGrading).
		Pluck("id", &ids).Error; err != nil {
		log.Printf("requeue stuck sessions: %v", err)
		return
	}
	for _, id := range ids {
		g.Enqueue(id)
	}
	if len(ids) > 0 {
		log.Printf("requeued %d sessions for grading", len(ids))
	}
}

func (g *Grader) work() {
	for id := range g.queue {
		if err := g.GradeSession(id); err != nil {
			log.Printf("grading session %d failed: %v — retrying in 30s", id, err)
			time.AfterFunc(30*time.Second, func() { g.Enqueue(id) })
		}
	}
}

func (g *Grader) GradeSession(sessionID uint) error {
	var session models.ExamSession
	if err := g.db.First(&session, sessionID).Error; err != nil {
		return err
	}
	if session.Status != models.SessionGrading {
		return nil // already graded or still active
	}

	var setQuestions []models.SetQuestion
	if err := g.db.Where("set_id = ?", session.SetID).
		Preload("Question.TestCases").
		Order("position").
		Find(&setQuestions).Error; err != nil {
		return err
	}

	var answers []models.Answer
	if err := g.db.Where("session_id = ?", sessionID).Find(&answers).Error; err != nil {
		return err
	}
	answerByQ := map[uint]*models.Answer{}
	for i := range answers {
		answerByQ[answers[i].QuestionID] = &answers[i]
	}

	autoScore := 0.0
	for _, sq := range setQuestions {
		q := sq.Question
		ans := answerByQ[q.ID]
		switch q.Type {
		case models.QuestionAptitude:
			score := 0.0
			if ans != nil && ans.SelectedIndex != nil && q.CorrectIndex != nil && *ans.SelectedIndex == *q.CorrectIndex {
				score = float64(sq.Marks)
			}
			autoScore += score
			if ans != nil {
				if err := g.db.Model(ans).Updates(map[string]any{"score": score, "graded": true}).Error; err != nil {
					return err
				}
			}
		case models.QuestionCoding:
			if ans == nil || strings.TrimSpace(ans.Code) == "" {
				if ans != nil {
					if err := g.db.Model(ans).Updates(map[string]any{"score": 0, "graded": true}).Error; err != nil {
						return err
					}
				}
				continue
			}
			score, results, err := g.gradeCoding(ans, &q, sq.Marks)
			if err != nil {
				return fmt.Errorf("coding question %d: %w", q.ID, err)
			}
			autoScore += score
			if err := g.db.Model(ans).Select("Score", "Graded", "TestResults").
				Updates(models.Answer{Score: score, Graded: true, TestResults: results}).Error; err != nil {
				return err
			}
		case models.QuestionEnglish:
			// manual grading by admin
		}
	}

	var manual float64
	g.db.Model(&models.Answer{}).
		Where("session_id = ? AND type = ? AND graded = true", sessionID, models.QuestionEnglish).
		Select("COALESCE(SUM(score), 0)").Scan(&manual)

	return g.db.Model(&session).Updates(map[string]any{
		"status":       models.SessionGraded,
		"auto_score":   round2(autoScore),
		"manual_score": round2(manual),
		"total_score":  round2(autoScore + manual),
	}).Error
}

// gradeCoding runs the answer against every test case. A transport error to Piston
// aborts grading (caller retries); per-test runtime failures just fail that test.
func (g *Grader) gradeCoding(ans *models.Answer, q *models.Question, marks int) (float64, []models.TestResult, error) {
	lang := ans.Language
	if !models.CodingLanguages[lang] {
		lang = "python"
	}

	results := make([]models.TestResult, 0, len(q.TestCases))
	totalWeight, passedWeight := 0, 0
	compileFailed := false
	compileOutput := ""

	for i, tc := range q.TestCases {
		w := tc.Weight
		if w <= 0 {
			w = 1
		}
		totalWeight += w

		res := models.TestResult{
			Index:    i,
			Hidden:   tc.Hidden,
			Input:    truncate(tc.Input, maxStoredOutput),
			Expected: truncate(tc.Expected, maxStoredOutput),
			Weight:   w,
		}

		if compileFailed {
			res.Status = "compile_error"
			res.Actual = compileOutput
			results = append(results, res)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
		exec, err := g.runner.Execute(ctx, lang, ans.Code, tc.Input, q.TimeLimitMS, q.MemoryLimitKB)
		cancel()
		if err != nil {
			return 0, nil, err
		}

		switch {
		case !exec.CompileOK:
			compileFailed = true
			compileOutput = truncate(exec.CompileOutput, maxStoredOutput)
			res.Status = "compile_error"
			res.Actual = compileOutput
		case exec.TimedOut:
			res.Status = "timeout"
			res.Actual = truncate(exec.Stdout, maxStoredOutput)
		case CompareOutput(exec.Stdout, tc.Expected):
			res.Status = "passed"
			res.Passed = true
			passedWeight += w
		default:
			res.Status = "failed"
			res.Actual = truncate(exec.Output, maxStoredOutput)
		}
		results = append(results, res)
	}

	if totalWeight == 0 {
		return 0, results, nil
	}
	score := round2(float64(marks) * float64(passedWeight) / float64(totalWeight))
	return score, results, nil
}

// RecomputeManual refreshes a session's manual (English) score and total after admin grading.
func RecomputeManual(db *gorm.DB, sessionID uint) error {
	var manual float64
	if err := db.Model(&models.Answer{}).
		Where("session_id = ? AND type = ? AND graded = true", sessionID, models.QuestionEnglish).
		Select("COALESCE(SUM(score), 0)").Scan(&manual).Error; err != nil {
		return err
	}
	return db.Model(&models.ExamSession{}).Where("id = ?", sessionID).
		Updates(map[string]any{
			"manual_score": round2(manual),
			"total_score":  gorm.Expr("auto_score + ?", round2(manual)),
		}).Error
}

// NormalizeOutput trims trailing whitespace per line and surrounding blank lines,
// so cosmetic whitespace differences don't fail a test.
func NormalizeOutput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func CompareOutput(actual, expected string) bool {
	return NormalizeOutput(actual) == NormalizeOutput(expected)
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… (truncated)"
}
