package handlers

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type UsersHandler struct {
	db *db.Queries
}

type RegisterUserRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role" binding:"required,oneof=admin user"`
}

type CreateUserResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type LoginResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Token     string `json:"token"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func NewUsersHandler(dbconn *pgxpool.Pool) *UsersHandler {
	return &UsersHandler{
		db: db.New(dbconn),
	}
}

func (h *UsersHandler) Register(c *gin.Context) {
	// check JWT secret early (fail fast)
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// c.JSON(http.StatusInternalServerError, gin.H{
		// 	"error":   "Server misconfiguration: JWT secret not set",
		// 	"details": "Set JWT_SECRET environment variable",
		// })
		// return
		secret = "secret"
	}

	var req RegisterUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid input",
			"details": err.Error(),
		})
		return
	}

	// use GetUserByEmail to check existence first
	if existing, err := h.db.GetUserByEmail(context.Background(), req.Email); err == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "User already exists",
			"details": "A user with this email already exists",
			"user_id": existing.ID.String(),
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to hash password",
			"details": err.Error(),
		})
		return
	}

	params := db.CreateUserParams{
		Name:     req.Name,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     req.Role,
	}

	user, err := h.db.CreateUser(context.Background(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to create user",
			"details": err.Error(),
		})
		return
	}

	expiration := time.Now().Add(72 * time.Hour)

	claims := jwt.MapClaims{
		"sub":  user.ID.String(),
		"role": user.Role,
		"iat":  time.Now().Unix(),
		"exp":  expiration.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to generate token",
			"details": err.Error(),
		})
		return
	}

	response := CreateUserResponse{
		ID:        user.ID.String(),
		Name:      user.Name,
		Email:     user.Email,
		Role:      user.Role,
		Token:     signedToken,
		CreatedAt: user.CreatedAt.Time.String(),
		UpdatedAt: user.UpdatedAt.Time.String(),
	}

	c.JSON(http.StatusCreated, response)
}

func (h *UsersHandler) Login(c *gin.Context) {
	// check JWT secret early (fail fast)
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// c.JSON(http.StatusInternalServerError, gin.H{
		// 	"error":   "Server misconfiguration: JWT secret not set",
		// 	"details": "Set JWT_SECRET environment variable",
		// })
		// return
		secret = "secret"
		return
	}

	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid input",
			"details": err.Error(),
		})
		return
	}

	user, err := h.db.GetUserByEmail(context.Background(), req.Email)
	if err != nil {
		// do not reveal whether email exists; return generic unauthorized
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials",
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials",
		})
		return
	}

	expiration := time.Now().Add(72 * time.Hour)

	claims := jwt.MapClaims{
		"sub":  user.ID.String(),
		"role": user.Role,
		"iat":  time.Now().Unix(),
		"exp":  expiration.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to generate token",
			"details": err.Error(),
		})
		return
	}

	resp := LoginResponse{
		ID:        user.ID.String(),
		Name:      user.Name,
		Email:     user.Email,
		Role:      user.Role,
		Token:     signedToken,
		CreatedAt: user.CreatedAt.Time.String(),
		UpdatedAt: user.UpdatedAt.Time.String(),
	}

	c.JSON(http.StatusOK, resp)
}
