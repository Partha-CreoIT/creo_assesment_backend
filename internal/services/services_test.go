package services

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"exam_taker_bc/internal/models"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(models.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func makeExamWithSets(t *testing.T, db *gorm.DB) *models.Exam {
	t.Helper()
	exam := &models.Exam{Title: "T", DurationMin: 60, MaxViolations: 3, Status: models.ExamActive}
	if err := db.Create(exam).Error; err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"A", "B", "C", "D", "E", "F"} {
		if err := db.Create(&models.QuestionSet{ExamID: exam.ID, Label: l}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return exam
}

func TestNormalizeAndCompareOutput(t *testing.T) {
	cases := []struct {
		actual, expected string
		want             bool
	}{
		{"8\n", "8", true},
		{"8", "8\n", true},
		{"a b  \nc\t\n", "a b\nc", true},
		{"line1\r\nline2\r\n", "line1\nline2", true},
		{"\n\n8\n\n", "8", true},
		{"9", "8", false},
		{"a\nb", "a b", false},
		{"", "", true},
	}
	for _, c := range cases {
		if got := CompareOutput(c.actual, c.expected); got != c.want {
			t.Errorf("CompareOutput(%q, %q) = %v, want %v", c.actual, c.expected, got, c.want)
		}
	}
}

func TestAssignSetBalances(t *testing.T) {
	db := testDB(t)
	exam := makeExamWithSets(t, db)

	counts := map[string]int{}
	for i := 0; i < 60; i++ {
		student := models.Student{Name: "S", Email: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@x.com", Semester: 4}
		if err := db.Create(&student).Error; err != nil {
			t.Fatal(err)
		}
		set, err := AssignSet(db, exam.ID)
		if err != nil {
			t.Fatalf("assign %d: %v", i, err)
		}
		if err := db.Create(&models.ExamSession{
			Token: student.Email, StudentID: student.ID, ExamID: exam.ID, SetID: set.ID,
			Status: models.SessionActive,
		}).Error; err != nil {
			t.Fatal(err)
		}
		counts[set.Label]++
	}
	if len(counts) != 6 {
		t.Fatalf("expected all 6 sets used, got %v", counts)
	}
	for label, n := range counts {
		if n != 10 {
			t.Errorf("set %s got %d sessions, want 10 (even spread): %v", label, n, counts)
		}
	}
}

func seedBank(t *testing.T, db *gorm.DB, english, aptitude, coding int) {
	t.Helper()
	mk := func(qt models.QuestionType, n, marks int) {
		for i := 0; i < n; i++ {
			q := models.Question{Type: qt, Title: "q", Body: "b", Marks: marks}
			if qt == models.QuestionAptitude {
				q.Options = []string{"x", "y"}
				zero := 0
				q.CorrectIndex = &zero
			}
			if err := db.Create(&q).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	mk(models.QuestionEnglish, english, 5)
	mk(models.QuestionAptitude, aptitude, 2)
	mk(models.QuestionCoding, coding, 10)
}

func TestAutoDistributeUnique(t *testing.T) {
	db := testDB(t)
	exam := makeExamWithSets(t, db)
	seedBank(t, db, 6, 12, 6)

	err := AutoDistribute(db, exam.ID, DistributeCounts{English: 1, Aptitude: 2, Coding: 1}, DistributeUnique)
	if err != nil {
		t.Fatalf("unique distribute: %v", err)
	}
	var sets []models.QuestionSet
	db.Where("exam_id = ?", exam.ID).Find(&sets)
	seen := map[uint]bool{}
	for _, s := range sets {
		var sqs []models.SetQuestion
		db.Where("set_id = ?", s.ID).Order("position").Find(&sqs)
		if len(sqs) != 4 {
			t.Fatalf("set %s has %d questions, want 4", s.Label, len(sqs))
		}
		for _, sq := range sqs {
			if seen[sq.QuestionID] {
				t.Errorf("question %d appears in more than one set (unique mode)", sq.QuestionID)
			}
			seen[sq.QuestionID] = true
		}
	}
}

func TestAutoDistributeInsufficientPool(t *testing.T) {
	db := testDB(t)
	exam := makeExamWithSets(t, db)
	seedBank(t, db, 2, 2, 2)

	err := AutoDistribute(db, exam.ID, DistributeCounts{English: 1, Aptitude: 1, Coding: 1}, DistributeUnique)
	if err == nil {
		t.Fatal("expected error for insufficient pool in unique mode")
	}

	// shuffled mode should be fine with a small pool
	if err := AutoDistribute(db, exam.ID, DistributeCounts{English: 2, Aptitude: 2, Coding: 2}, DistributeShuffled); err != nil {
		t.Fatalf("shuffled distribute: %v", err)
	}
	var n int64
	db.Model(&models.SetQuestion{}).Count(&n)
	if n != 36 {
		t.Fatalf("expected 36 set questions (6 per set), got %d", n)
	}
}

func TestFinalizeSessionOnlyOnce(t *testing.T) {
	db := testDB(t)
	exam := makeExamWithSets(t, db)
	student := models.Student{Name: "S", Email: "s@x.com", Semester: 4}
	db.Create(&student)
	var set models.QuestionSet
	db.Where("exam_id = ?", exam.ID).First(&set)
	session := models.ExamSession{Token: "tok", StudentID: student.ID, ExamID: exam.ID, SetID: set.ID, Status: models.SessionActive}
	db.Create(&session)

	grader := NewGrader(db, nil)
	if err := FinalizeSession(db, grader, session.ID, models.SubmitAutoViolation); err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	if err := FinalizeSession(db, grader, session.ID, models.SubmitManual); err != ErrNotActive {
		t.Fatalf("second finalize should return ErrNotActive, got %v", err)
	}
	var fresh models.ExamSession
	db.First(&fresh, session.ID)
	if fresh.Status != models.SessionGrading || fresh.SubmitKind != models.SubmitAutoViolation || !fresh.Flagged {
		t.Fatalf("unexpected session state: %+v", fresh)
	}
}

func TestGradeSessionMCQ(t *testing.T) {
	db := testDB(t)
	exam := makeExamWithSets(t, db)
	var set models.QuestionSet
	db.Where("exam_id = ?", exam.ID).First(&set)

	one := 1
	q1 := models.Question{Type: models.QuestionAptitude, Title: "q1", Body: "b", Marks: 2, Options: []string{"a", "b"}, CorrectIndex: &one}
	q2 := models.Question{Type: models.QuestionAptitude, Title: "q2", Body: "b", Marks: 3, Options: []string{"a", "b"}, CorrectIndex: &one}
	db.Create(&q1)
	db.Create(&q2)
	db.Create(&models.SetQuestion{SetID: set.ID, QuestionID: q1.ID, Position: 0, Marks: 2})
	db.Create(&models.SetQuestion{SetID: set.ID, QuestionID: q2.ID, Position: 1, Marks: 3})

	student := models.Student{Name: "S", Email: "g@x.com", Semester: 4}
	db.Create(&student)
	session := models.ExamSession{Token: "tok2", StudentID: student.ID, ExamID: exam.ID, SetID: set.ID, Status: models.SessionGrading}
	db.Create(&session)

	right, wrong := 1, 0
	db.Create(&models.Answer{SessionID: session.ID, QuestionID: q1.ID, Type: models.QuestionAptitude, SelectedIndex: &right})
	db.Create(&models.Answer{SessionID: session.ID, QuestionID: q2.ID, Type: models.QuestionAptitude, SelectedIndex: &wrong})

	grader := NewGrader(db, nil)
	if err := grader.GradeSession(session.ID); err != nil {
		t.Fatalf("grade: %v", err)
	}
	var fresh models.ExamSession
	db.First(&fresh, session.ID)
	if fresh.Status != models.SessionGraded {
		t.Fatalf("status = %s, want graded", fresh.Status)
	}
	if fresh.AutoScore != 2 || fresh.TotalScore != 2 {
		t.Fatalf("autoScore = %v totalScore = %v, want 2 and 2", fresh.AutoScore, fresh.TotalScore)
	}
}
