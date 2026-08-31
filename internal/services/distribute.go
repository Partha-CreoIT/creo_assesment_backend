package services

import (
	"errors"
	"fmt"
	"math/rand"

	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

type DistributeCounts struct {
	English  int `json:"english"`
	Aptitude int `json:"aptitude"`
	Coding   int `json:"coding"`
}

const (
	// DistributeUnique gives every set different questions (needs a pool of count × sets per type).
	DistributeUnique = "unique"
	// DistributeShuffled gives every set the same sampled questions in a different order.
	DistributeShuffled = "shuffled"
)

var ErrBadDistribute = errors.New("bad distribute request")

// AutoDistribute fills all of an exam's sets (A–F) from the question bank.
// Section order inside each set is always English → Aptitude → Coding.
func AutoDistribute(db *gorm.DB, examID uint, counts DistributeCounts, mode string) error {
	if mode != DistributeUnique && mode != DistributeShuffled {
		return fmt.Errorf("%w: mode must be unique or shuffled", ErrBadDistribute)
	}
	if counts.English < 0 || counts.Aptitude < 0 || counts.Coding < 0 ||
		counts.English+counts.Aptitude+counts.Coding == 0 {
		return fmt.Errorf("%w: at least one section count must be positive", ErrBadDistribute)
	}

	var sets []models.QuestionSet
	if err := db.Where("exam_id = ?", examID).Order("label").Find(&sets).Error; err != nil {
		return err
	}
	if len(sets) == 0 {
		return fmt.Errorf("%w: exam has no sets", ErrBadDistribute)
	}

	sections := []struct {
		qType models.QuestionType
		count int
	}{
		{models.QuestionEnglish, counts.English},
		{models.QuestionAptitude, counts.Aptitude},
		{models.QuestionCoding, counts.Coding},
	}

	// perSet[setIdx] = ordered questions for that set
	perSet := make([][]models.Question, len(sets))

	for _, sec := range sections {
		if sec.count == 0 {
			continue
		}
		var pool []models.Question
		if err := db.Select("id", "marks", "type").Where("type = ?", sec.qType).Find(&pool).Error; err != nil {
			return err
		}
		rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

		switch mode {
		case DistributeUnique:
			need := sec.count * len(sets)
			if len(pool) < need {
				return fmt.Errorf("%w: need %d %s questions for unique sets, bank has %d",
					ErrBadDistribute, need, sec.qType, len(pool))
			}
			for i := range sets {
				perSet[i] = append(perSet[i], pool[i*sec.count:(i+1)*sec.count]...)
			}
		case DistributeShuffled:
			if len(pool) < sec.count {
				return fmt.Errorf("%w: need %d %s questions, bank has %d",
					ErrBadDistribute, sec.count, sec.qType, len(pool))
			}
			sample := pool[:sec.count]
			for i := range sets {
				block := make([]models.Question, len(sample))
				copy(block, sample)
				rand.Shuffle(len(block), func(x, y int) { block[x], block[y] = block[y], block[x] })
				perSet[i] = append(perSet[i], block...)
			}
		}
	}

	return db.Transaction(func(tx *gorm.DB) error {
		setIDs := make([]uint, len(sets))
		for i, s := range sets {
			setIDs[i] = s.ID
		}
		if err := tx.Where("set_id IN ?", setIDs).Delete(&models.SetQuestion{}).Error; err != nil {
			return err
		}
		var rows []models.SetQuestion
		for i, s := range sets {
			for pos, q := range perSet[i] {
				rows = append(rows, models.SetQuestion{
					SetID:      s.ID,
					QuestionID: q.ID,
					Position:   pos,
					Marks:      q.Marks,
				})
			}
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}
