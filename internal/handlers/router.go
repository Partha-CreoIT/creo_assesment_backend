package handlers

import (
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"exam_taker_bc/internal/middleware"
)

func (a *API) BuildRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	r.Use(cors.New(cors.Config{
		AllowOrigins:     a.Cfg.CORSOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Disposition"},
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "serverNow": time.Now().UTC()})
	})

	v1 := r.Group("/api/v1")

	// public / student entry
	v1.GET("/exam/active", a.ActiveExam)
	v1.POST("/sessions", a.Register)

	// student (session token)
	me := v1.Group("/me", middleware.StudentAuth(a.DB))
	me.GET("", a.Me)
	me.GET("/paper", a.Paper)
	me.PUT("/answers/:questionId", a.UpsertAnswer)
	me.POST("/violations", a.ReportViolation)
	me.POST("/run", a.RunCode)
	me.POST("/submit", a.Submit)
	me.POST("/heartbeat", a.Heartbeat)

	// admin
	v1.POST("/admin/login", a.AdminLogin)
	admin := v1.Group("/admin", middleware.AdminAuth(a.Cfg.JWTSecret))
	admin.GET("/questions", a.ListQuestions)
	admin.POST("/questions", a.CreateQuestion)
	admin.GET("/questions/:id", a.GetQuestion)
	admin.PUT("/questions/:id", a.UpdateQuestion)
	admin.DELETE("/questions/:id", a.DeleteQuestion)
	admin.GET("/questions/template", a.DownloadQuestionTemplate)
	admin.POST("/exams/:id/questions/upload", a.UploadQuestions)

	admin.GET("/exams", a.ListExams)
	admin.POST("/exams", a.CreateExam)
	admin.GET("/exams/:id", a.GetExam)
	admin.PUT("/exams/:id", a.UpdateExam)
	admin.DELETE("/exams/:id", a.DeleteExam)
	admin.POST("/exams/:id/activate", a.ActivateExam)
	admin.POST("/exams/:id/close", a.CloseExam)
	admin.POST("/exams/:id/auto-distribute", a.AutoDistribute)
	admin.GET("/exams/:id/sessions", a.MonitorSessions)
	admin.GET("/exams/:id/export", a.ExportCSV)

	admin.PUT("/sets/:id/questions", a.UpdateSetQuestions)
	admin.GET("/sessions/:id", a.SessionDetail)
	admin.PUT("/answers/:id/grade", a.GradeAnswer)

	return r
}
