package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"

	"github.com/Linka-masterskaya/zip-backend/pkg/linka/cryptox"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	bcryptCost        int    = 12
	RoleDefectologist string = "defectologist"
)

type Service struct {
	repo   *authRepo
	crypto *cryptox.Crypto
	// mailer domain.EmailSender
}

func NewService(crypto *cryptox.Crypto, repo *authRepo) *Service {
	return &Service{crypto: crypto, repo: repo}
}

func (s *Service) Register(ctx context.Context, req RegisterRequest) (*RegisterResponse, error) {

	email := strings.TrimSpace(strings.ToLower(req.Email))
	emailHash := s.crypto.Hash([]byte(email))

	exists, err := s.repo.EmailExists(ctx, emailHash)

	if err != nil {
		return nil, err
	}

	if exists {
		return nil, ErrEmailAlreadyExists
	}

	emailEncrypted, err := s.crypto.Encrypt([]byte(email))

	if err != nil {
		return nil, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)

	if err != nil {
		return nil, err
	}

	tx, err := s.repo.pool.Begin(ctx)

	if err != nil {
		return nil, err
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	//начало транзакции
	txRepo := s.repo.withTx(tx)

	// добавление пользователя
	userParams := CreateUserParams{ID: uuid.New(), OrganizationID: uuid.New(), Name: ""}

	err = txRepo.CreateUser(ctx, userParams)

	if err != nil {
		return nil, err
	}

	//регистрация auth_cred
	credParams := CreateAuthCredParams{
		UserID:         userParams.ID,
		EmailHash:      emailHash,
		EmailEncrypted: emailEncrypted,
		PasswordHash:   string(passwordHash),
		Role:           RoleDefectologist,
	}

	err = txRepo.CreateAuthCred(ctx, credParams)

	if err != nil {
		return nil, err
	}

	verifyToken := make([]byte, 32)

	if _, err = rand.Read(verifyToken); err != nil {
		return nil, err
	}

	verifyTokenString := base64.RawURLEncoding.EncodeToString(verifyToken)
	// TODO: ВРЕМЕННО. Нужно передать в mailer
	_ = verifyTokenString

	tokenHash := sha256.Sum256(verifyToken)

	verifyParams := CreateVerifyTokenParams{
		ID:        uuid.New(),
		UserID:    userParams.ID,
		TokenHash: tokenHash[:],
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	err = txRepo.CreateVerifyToken(ctx, verifyParams)

	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &RegisterResponse{
		AccessToken: "stub",
	}, nil
}
