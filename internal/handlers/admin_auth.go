package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"exam_taker_bc/internal/middleware"
	"exam_taker_bc/internal/models"
)

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) AdminLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var admin models.Admin
	if err := a.DB.Where("email = ?", strings.ToLower(strings.TrimSpace(req.Email))).First(&admin).Error; err != nil {
		fail(c, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)) != nil {
		fail(c, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, err := middleware.NewAdminToken(a.Cfg.JWTSecret, admin.ID)
	if err != nil {
		failServer(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "email": admin.Email})
}
