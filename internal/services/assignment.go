package services

import (
	"errors"
	"math/rand"

	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

// AssignSet picks the least-assigned set (A–F) for the exam, breaking ties randomly,
// so students are spread evenly across sets.
func AssignSet(db *gorm.DB, examID uint) (*models.QuestionSet, error) {
	var sets []models.QuestionSet
	if err := db.Where("exam_id = ?", examID).Order("label").Find(&sets).Error; err != nil {
		return nil, err
	}
	if len(sets) == 0 {
		return nil, errors.New("exam has no question sets")
	}

	type row struct {
		SetID uint
		N     int64
	}
	var rows []row
	if err := db.Model(&models.ExamSession{}).
		Select("set_id, count(*) as n").
		Where("exam_id = ?", examID).
		Group("set_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := map[uint]int64{}
	for _, r := range rows {
		counts[r.SetID] = r.N
	}

	minCount := int64(-1)
	for _, s := range sets {
		if minCount == -1 || counts[s.ID] < minCount {
			minCount = counts[s.ID]
		}
	}
	var candidates []models.QuestionSet
	for _, s := range sets {
		if counts[s.ID] == minCount {
			candidates = append(candidates, s)
		}
	}
	picked := candidates[rand.Intn(len(candidates))]
	return &picked, nil
}
