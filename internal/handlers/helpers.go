package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"exam_taker_bc/internal/config"
	"exam_taker_bc/internal/runner"
	"exam_taker_bc/internal/services"
)

// API bundles the dependencies every handler needs.
type API struct {
	DB         *gorm.DB
	Cfg        *config.Config
	Grader     *services.Grader
	Runner     runner.Runner
	RunLimiter *RunLimiter
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"error": msg})
}

func failServer(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error: " + err.Error()})
}

func paramUint(c *gin.Context, name string) (uint, bool) {
	id, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return uint(id), true
}
