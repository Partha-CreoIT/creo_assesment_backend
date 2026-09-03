// growth bulk-inserts synthetic students/sessions/answers directly via SQL
// batches (bypassing the HTTP API entirely, for speed) to simulate a
// college's accumulated database size after many exams over time — Test 39's
// "10k/100k/1M answers" scale check. Measure API latency before and after
// with repeated timed calls against a real endpoint, e.g.:
//
//	for i in 1 2 3; do curl -s -o /dev/null -w "%{time_total}\n" \
//	  -H "Authorization: Bearer $ADMIN_TOKEN" $BASE_URL/api/v1/admin/exams/$EXAM_ID/sessions; done
//
// Usage:
//
//	go run ./loadtest/growth --exam-id=5 --target-answers=100000
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/database"
	"exam_taker_bc/internal/models"
)

func main() {
	examID := flag.Uint("exam-id", 0, "exam ID whose sets/questions to attach synthetic answers to (required)")
	targetAnswers := flag.Int("target-answers", 10000, "approximate Answer row count to reach (existing rows for this exam count toward it)")
	batchSessions := flag.Int("batch-sessions", 500, "synthetic sessions created per batch (3 bulk INSERTs per batch: students, sessions, answers)")
	flag.Parse()

	if *examID == 0 {
		log.Fatal("growth: --exam-id is required")
	}

	cfg := config.Load()
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("growth: connect db: %v", err)
	}

	var sets []models.QuestionSet
	if err := db.Where("exam_id = ?", *examID).Find(&sets).Error; err != nil {
		log.Fatalf("growth: load sets: %v", err)
	}
	if len(sets) == 0 {
		log.Fatalf("growth: exam %d has no sets — create/populate it first (e.g. via lib/setup.js's setupExam)", *examID)
	}

	var setQuestions []models.SetQuestion
	setIDs := make([]uint, len(sets))
	for i, s := range sets {
		setIDs[i] = s.ID
	}
	if err := db.Preload("Question").Where("set_id IN ?", setIDs).Find(&setQuestions).Error; err != nil {
		log.Fatalf("growth: load set questions: %v", err)
	}
	if len(setQuestions) == 0 {
		log.Fatalf("growth: exam %d's sets have no questions assigned", *examID)
	}
	questionsBySet := map[uint][]models.SetQuestion{}
	for _, sq := range setQuestions {
		questionsBySet[sq.SetID] = append(questionsBySet[sq.SetID], sq)
	}

	var existing int64
	db.Model(&models.Answer{}).
		Joins("JOIN exam_sessions es ON es.id = answers.session_id").
		Where("es.exam_id = ?", *examID).Count(&existing)
	need := int64(*targetAnswers) - existing
	if need <= 0 {
		fmt.Printf("growth: already at or above target (%d existing answer rows >= %d target)\n", existing, *targetAnswers)
		return
	}

	avgQuestionsPerSet := len(setQuestions) / len(sets)
	if avgQuestionsPerSet == 0 {
		avgQuestionsPerSet = 1
	}
	sessionsNeeded := int(need)/avgQuestionsPerSet + 1

	fmt.Printf("growth: exam %d has %d existing answer rows, target %d — creating ~%d synthetic sessions (~%d answers/session)\n",
		*examID, existing, *targetAnswers, sessionsNeeded, avgQuestionsPerSet)

	start := time.Now()
	totalAnswers := 0
	for base := 0; base < sessionsNeeded; base += *batchSessions {
		n := *batchSessions
		if base+n > sessionsNeeded {
			n = sessionsNeeded - base
		}

		students := make([]models.Student, n)
		for i := 0; i < n; i++ {
			idx := base + i
			students[i] = models.Student{
				Name:     fmt.Sprintf("Growth Synthetic %d", idx),
				Email:    fmt.Sprintf("growth-synthetic-exam%d-%d@test.local", *examID, idx),
				Semester: (idx % 8) + 1,
				Phone:    "9000000000",
			}
		}

		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.CreateInBatches(&students, 200).Error; err != nil {
				return fmt.Errorf("create students: %w", err)
			}

			sessions := make([]models.ExamSession, n)
			now := time.Now().UTC()
			for i := 0; i < n; i++ {
				set := sets[rand.Intn(len(sets))]
				sessions[i] = models.ExamSession{
					Token:       fmt.Sprintf("growth-exam%d-%d-%d", *examID, base+i, now.UnixNano()+int64(i)),
					StudentID:   students[i].ID,
					ExamID:      *examID,
					SetID:       set.ID,
					Status:      models.SessionGraded,
					SubmitKind:  models.SubmitManual,
					StartedAt:   now,
					EndsAt:      now,
					SubmittedAt: &now,
					LastSeenAt:  now,
				}
			}
			if err := tx.CreateInBatches(&sessions, 200).Error; err != nil {
				return fmt.Errorf("create sessions: %w", err)
			}

			var answers []models.Answer
			for i := 0; i < n; i++ {
				setID := sessions[i].SetID
				for _, sq := range questionsBySet[setID] {
					ans := models.Answer{
						SessionID:  sessions[i].ID,
						QuestionID: sq.QuestionID,
						Type:       sq.Question.Type,
						AnswerText: "synthetic growth-test data",
						Score:      float64(sq.Marks),
						Graded:     true,
					}
					answers = append(answers, ans)
				}
			}
			if len(answers) > 0 {
				if err := tx.CreateInBatches(&answers, 500).Error; err != nil {
					return fmt.Errorf("create answers: %w", err)
				}
			}
			totalAnswers += len(answers)
			return nil
		}); err != nil {
			log.Fatalf("growth: batch at session %d failed: %v", base, err)
		}

		fmt.Printf("growth: %d/%d sessions, %d answers inserted (%.1fs elapsed)\n",
			base+n, sessionsNeeded, totalAnswers, time.Since(start).Seconds())
	}

	fmt.Printf("growth: done — %d synthetic answer rows inserted in %.1fs\n", totalAnswers, time.Since(start).Seconds())
}
