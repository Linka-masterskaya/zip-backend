package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func (s *Service) GenerateJWT(user *User, cred *UserCred) (string, error) {
	claims := jwt.MapClaims{
		"sub":   user.ID.String(),
		"email": cred.EmailEncrypted,
		"role":  cred.Role,
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}
