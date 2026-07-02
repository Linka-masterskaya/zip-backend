package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func (s *Service) GenerateJWT(user *User, cred *UserCred) (string, error) {
	email, err := s.crypto.Decrypt(cred.EmailEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt email: %w", err)
	}
	claims := jwt.MapClaims{
		"sub":   user.ID.String(),
		"email": email,
		"role":  cred.Role,
		"exp":   time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}
