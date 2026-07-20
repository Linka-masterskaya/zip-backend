package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockTx struct {
	mock.Mock
}

func (m *MockTx) Begin(ctx context.Context) (pgx.Tx, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(pgx.Tx), args.Error(1)
}

func (m *MockTx) Commit(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTx) Rollback(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTx) Conn() *pgx.Conn {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*pgx.Conn)
}

func (m *MockTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	args := m.Called(ctx, tableName, columnNames, rowSrc)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	args := m.Called(ctx, sql, arguments)
	if args.Get(0) == nil {
		return pgconn.CommandTag{}, args.Error(1)
	}
	return args.Get(0).(pgconn.CommandTag), args.Error(1)
}

func (m *MockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	callArgs := m.Called(ctx, sql, args)
	if callArgs.Get(0) == nil {
		return nil, callArgs.Error(1)
	}
	return callArgs.Get(0).(pgx.Rows), callArgs.Error(1)
}

func (m *MockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	callArgs := m.Called(ctx, sql, args)
	if callArgs.Get(0) == nil {
		return nil
	}
	return callArgs.Get(0).(pgx.Row)
}

func (m *MockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	args := m.Called(ctx, b)
	return args.Get(0).(pgx.BatchResults)
}

func (m *MockTx) LargeObjects() pgx.LargeObjects {
	args := m.Called()
	return args.Get(0).(pgx.LargeObjects)
}

func (m *MockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	args := m.Called(ctx, name, sql)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*pgconn.StatementDescription), args.Error(1)
}

type MockRepository struct {
	mock.Mock
}

func (m *MockRepository) FindIdentityByProviderUID(ctx context.Context, provider, providerUID string) (*UserIdentity, error) {
	args := m.Called(ctx, provider, providerUID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserIdentity), args.Error(1)
}

func (m *MockRepository) FindUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*User), args.Error(1)
}

func (m *MockRepository) FindUserCredByEmailHash(ctx context.Context, emailHash []byte) (*UserCred, error) {
	args := m.Called(ctx, emailHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserCred), args.Error(1)
}

func (m *MockRepository) FindUserCredByUserID(ctx context.Context, userID uuid.UUID) (*UserCred, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*UserCred), args.Error(1)
}

func (m *MockRepository) CreateUser(ctx context.Context, params CreateUserParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockRepository) CreateAuthCred(ctx context.Context, params CreateAuthCredParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockRepository) CreateIdentity(ctx context.Context, identity *UserIdentity) error {
	args := m.Called(ctx, identity)
	return args.Error(0)
}

func (m *MockRepository) UpdateUser(ctx context.Context, user *User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockRepository) Begin(ctx context.Context) (pgx.Tx, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(pgx.Tx), args.Error(1)
}

func (m *MockRepository) withTx(tx pgx.Tx) RepositoryInterface {
	args := m.Called(tx)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(RepositoryInterface)
}

type MockCrypto struct {
	mock.Mock
}

func (m *MockCrypto) Hash(data []byte) []byte {
	args := m.Called(data)
	return args.Get(0).([]byte)
}

func (m *MockCrypto) Encrypt(data []byte) ([]byte, error) {
	args := m.Called(data)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockCrypto) Decrypt(data []byte) ([]byte, error) {
	args := m.Called(data)
	return args.Get(0).([]byte), args.Error(1)
}

// ============= СУЩЕСТВУЮЩИЕ ТЕСТЫ =============

func TestUpsertUser_ExistingYandexIdentity(t *testing.T) {
	ctx := context.Background()
	email := "user@yandex.ru"
	name := "Олег Рожков"
	yandexID := "1234567890"
	userID := uuid.New()

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	identity := &UserIdentity{
		ID:          uuid.New(),
		UserID:      userID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}
	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(identity, nil)

	user := &User{
		ID:   userID,
		Name: "Старое имя",
	}
	mockRepo.On("FindUserByID", ctx, userID).Return(user, nil)
	mockRepo.On("UpdateUser", ctx, mock.AnythingOfType("*auth.User")).Return(nil)

	cred := &UserCred{
		UserID: userID,
		Role:   "viewer",
	}
	mockRepo.On("FindUserCredByUserID", ctx, userID).Return(cred, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.NoError(t, err)
	assert.Equal(t, name, resultUser.Name)
	assert.Equal(t, cred, resultCred)
	mockRepo.AssertExpectations(t)
}

func TestUpsertUser_EmailAlreadyRegistered_BlocksTakeover(t *testing.T) {
	ctx := context.Background()
	email := "victim@mail.ru"
	name := "Хакер"
	yandexID := "hacker-yandex-id"

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(nil, nil)

	emailHash := []byte("hashed_email")
	mockCrypto.On("Hash", []byte(email)).Return(emailHash)

	existingCred := &UserCred{
		UserID: uuid.New(),
		Role:   "viewer",
	}
	mockRepo.On("FindUserCredByEmailHash", ctx, emailHash).Return(existingCred, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.Error(t, err)
	assert.Nil(t, resultUser)
	assert.Nil(t, resultCred)
	assert.Contains(t, err.Error(), "already registered")

	mockRepo.AssertExpectations(t)
}

func TestUpsertUser_NewUser_CreatesEverything(t *testing.T) {
	ctx := context.Background()
	email := "newuser@mail.ru"
	name := "Новый Пользователь"
	yandexID := "new-yandex-id"

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(nil, nil)

	emailHash := []byte("hashed_email")
	mockCrypto.On("Hash", []byte(email)).Return(emailHash)
	mockRepo.On("FindUserCredByEmailHash", ctx, emailHash).Return(nil, nil)

	// Настройка транзакции с моком MockTx
	mockTx := new(MockTx)
	mockTx.On("Rollback", ctx).Return(nil)
	mockTx.On("Commit", ctx).Return(nil)

	mockRepo.On("Begin", ctx).Return(mockTx, nil)
	mockRepo.On("withTx", mockTx).Return(mockRepo)

	mockRepo.On("CreateUser", ctx, mock.AnythingOfType("auth.CreateUserParams")).Return(nil)

	encryptedEmail := []byte("encrypted_email")
	mockCrypto.On("Encrypt", []byte(email)).Return(encryptedEmail, nil)

	mockRepo.On("CreateAuthCred", ctx, mock.AnythingOfType("auth.CreateAuthCredParams")).Return(nil)
	mockRepo.On("CreateIdentity", ctx, mock.AnythingOfType("*auth.UserIdentity")).Return(nil)

	userID := uuid.New()
	newUser := &User{
		ID:   userID,
		Name: name,
	}
	mockRepo.On("FindUserByID", ctx, mock.AnythingOfType("uuid.UUID")).Return(newUser, nil)

	newCred := &UserCred{
		UserID: userID,
		Role:   "viewer",
	}
	mockRepo.On("FindUserCredByUserID", ctx, mock.AnythingOfType("uuid.UUID")).Return(newCred, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.NoError(t, err)
	assert.Equal(t, name, resultUser.Name)
	assert.Equal(t, "viewer", resultCred.Role)

	mockRepo.AssertExpectations(t)
}

func TestUpsertUser_IdentityExistsButUserNotFound(t *testing.T) {
	ctx := context.Background()
	email := "user@yandex.ru"
	name := "Олег"
	yandexID := "1234567890"
	userID := uuid.New()

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	identity := &UserIdentity{
		ID:          uuid.New(),
		UserID:      userID,
		Provider:    "yandex",
		ProviderUID: yandexID,
	}
	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(identity, nil)

	mockRepo.On("FindUserByID", ctx, userID).Return(nil, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.Error(t, err)
	assert.Nil(t, resultUser)
	assert.Nil(t, resultCred)
	assert.Contains(t, err.Error(), "user not found for identity")

	mockRepo.AssertExpectations(t)
}

func TestUpsertUser_DatabaseError_OnFindIdentity(t *testing.T) {
	ctx := context.Background()
	email := "user@yandex.ru"
	name := "Олег"
	yandexID := "1234567890"

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(nil, errors.New("database connection lost"))

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.Error(t, err)
	assert.Nil(t, resultUser)
	assert.Nil(t, resultCred)
	assert.Contains(t, err.Error(), "find identity by yandex_id")

	mockRepo.AssertExpectations(t)
}

// TestUpsertUser_NewUser_NoPanicOnNilIdentity проверяет, что при identity=nil не происходит паники
func TestUpsertUser_NewUser_NoPanicOnNilIdentity(t *testing.T) {
	ctx := context.Background()
	email := "newuser@mail.ru"
	name := "Новый Пользователь"
	yandexID := "new-yandex-id"

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	// Возвращаем nil, nil для несуществующего identity
	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(nil, nil)

	emailHash := []byte("hashed_email")
	mockCrypto.On("Hash", []byte(email)).Return(emailHash)
	mockRepo.On("FindUserCredByEmailHash", ctx, emailHash).Return(nil, nil)

	// Настройка транзакции
	mockTx := new(MockTx)
	mockTx.On("Rollback", ctx).Return(nil)
	mockTx.On("Commit", ctx).Return(nil)

	mockRepo.On("Begin", ctx).Return(mockTx, nil)
	mockRepo.On("withTx", mockTx).Return(mockRepo)

	mockRepo.On("CreateUser", ctx, mock.AnythingOfType("auth.CreateUserParams")).Return(nil)

	encryptedEmail := []byte("encrypted_email")
	mockCrypto.On("Encrypt", []byte(email)).Return(encryptedEmail, nil)

	mockRepo.On("CreateAuthCred", ctx, mock.AnythingOfType("auth.CreateAuthCredParams")).Return(nil)
	mockRepo.On("CreateIdentity", ctx, mock.AnythingOfType("*auth.UserIdentity")).Return(nil)

	userID := uuid.New()
	newUser := &User{
		ID:   userID,
		Name: name,
	}
	mockRepo.On("FindUserByID", ctx, mock.AnythingOfType("uuid.UUID")).Return(newUser, nil)

	newCred := &UserCred{
		UserID: userID,
		Role:   "viewer",
	}
	mockRepo.On("FindUserCredByUserID", ctx, mock.AnythingOfType("uuid.UUID")).Return(newCred, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.NoError(t, err)
	assert.Equal(t, name, resultUser.Name)
	assert.Equal(t, "viewer", resultCred.Role)

	mockRepo.AssertExpectations(t)
}

// TestUpsertUser_EmailAlreadyRegistered_ReturnsSentinelError проверяет корректный 409-маппинг
func TestUpsertUser_EmailAlreadyRegistered_ReturnsSentinelError(t *testing.T) {
	ctx := context.Background()
	email := "existing@mail.ru"
	name := "Пользователь"
	yandexID := "yandex-id"

	mockRepo := new(MockRepository)
	mockCrypto := new(MockCrypto)

	mockRepo.On("FindIdentityByProviderUID", ctx, "yandex", yandexID).Return(nil, nil)

	emailHash := []byte("hashed_email")
	mockCrypto.On("Hash", []byte(email)).Return(emailHash)

	existingCred := &UserCred{
		UserID: uuid.New(),
		Role:   "viewer",
	}
	mockRepo.On("FindUserCredByEmailHash", ctx, emailHash).Return(existingCred, nil)

	service := NewService(mockRepo, mockRepo, mockCrypto, "test-secret")

	resultUser, resultCred, err := service.UpsertUser(ctx, email, name, yandexID)

	require.Error(t, err)
	assert.Nil(t, resultUser)
	assert.Nil(t, resultCred)
	// Проверяем, что возвращается именно сентинел-ошибка
	assert.True(t, errors.Is(err, ErrEmailAlreadyRegistered))
	assert.Contains(t, err.Error(), "already registered")

	mockRepo.AssertExpectations(t)
}
