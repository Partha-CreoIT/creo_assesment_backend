package services

import (
	"errors"
	"log"
	"time"

	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

var ErrNotActive = errors.New("session is not active")

// FinalizeSession atomically moves an active session to 'grading' and enqueues it.
// Safe to call concurrently — only the first caller wins.
func FinalizeSession(db *gorm.DB, grader *Grader, sessionID uint, kind string) error {
	res := db.Model(&models.ExamSession{}).
		Where("id = ? AND status = ?", sessionID, models.SessionActive).
		Updates(map[string]any{
			"status":       models.SessionGrading,
			"submit_kind":  kind,
			"submitted_at": time.Now().UTC(),
			"flagged":      gorm.Expr("flagged OR ?", kind == models.SubmitAutoViolation),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotActive
	}
	grader.Enqueue(sessionID)
	return nil
}

// StartSweeper auto-submits sessions whose time expired (plus grace) every 30s,
// so a closed laptop can't keep a session alive forever.
func StartSweeper(db *gorm.DB, grader *Grader, graceSec int) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		for range ticker.C {
			cutoff := time.Now().UTC().Add(-time.Duration(graceSec) * time.Second)
			var ids []uint
			if err := db.Model(&models.ExamSession{}).
				Where("status = ? AND ends_at < ?", models.SessionActive, cutoff).
				Pluck("id", &ids).Error; err != nil {
				log.Printf("sweeper query: %v", err)
				continue
			}
			for _, id := range ids {
				if err := FinalizeSession(db, grader, id, models.SubmitAutoTime); err != nil && !errors.Is(err, ErrNotActive) {
					log.Printf("sweeper finalize %d: %v", id, err)
				} else if err == nil {
					log.Printf("sweeper auto-submitted session %d (time expired)", id)
				}
			}
		}
	}()
}
