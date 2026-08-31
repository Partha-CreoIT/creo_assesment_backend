package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"

	"exam_taker_bc/internal/models"
)

const SessionKey = "examSession"

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func NewAdminToken(secret string, adminID uint) (string, error) {
	claims := jwt.MapClaims{
		"sub":  float64(adminID),
		"role": "admin",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func AdminAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := bearerToken(c)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok || claims["role"] != "admin" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if sub, ok := claims["sub"].(float64); ok {
			c.Set("adminID", uint(sub))
		}
		c.Next()
	}
}

// StudentAuth resolves the opaque session token to an ExamSession (with Exam preloaded).
func StudentAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing session token"})
			return
		}
		var session models.ExamSession
		err := db.Preload("Exam").Preload("Student").Where("token = ?", token).First(&session).Error
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid session"})
			return
		}
		c.Set(SessionKey, &session)
		c.Next()
	}
}

func GetSession(c *gin.Context) *models.ExamSession {
	v, _ := c.Get(SessionKey)
	s, _ := v.(*models.ExamSession)
	return s
}
