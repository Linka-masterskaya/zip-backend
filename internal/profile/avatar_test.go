// internal/profile/avatar_test.go
package profile

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/Linka-masterskaya/zip-backend/internal/cryptox"
	"github.com/Linka-masterskaya/zip-backend/internal/mailer"

	"github.com/Linka-masterskaya/zip-backend/internal/middleware"
	"github.com/Linka-masterskaya/zip-backend/internal/reqctx"
	"github.com/Linka-masterskaya/zip-backend/internal/storage"
)

// fakeEmailSender implements EmailSender interface for tests.
type fakeEmailSender struct{}

func (f *fakeEmailSender) Send(ctx context.Context, to string, tmpl mailer.Template, data mailer.EmailData) error {
	return nil
}

// newTestCryptox creates a real cryptox instance for tests.
func newTestCryptox(t *testing.T) *cryptox.Cryptox {
	t.Helper()
	// Use fixed keys for deterministic tests
	aesKey := make([]byte, 32)
	hmacKey := make([]byte, 32)
	for i := range aesKey {
		aesKey[i] = byte(i)
		hmacKey[i] = byte(i + 32)
	}
	crypto, err := cryptox.New(aesKey, hmacKey)
	require.NoError(t, err)
	return crypto
}

// defaultEmailConfig returns default config for tests.
func defaultEmailConfig() EmailConfig {
	return EmailConfig{
		EmailChangeTTL: 24 * time.Hour,
		EmailVerifyTTL: 24 * time.Hour,
	}
}

type testRevoker struct {
	revokeErr    error
	revokedID    string
	revokeCalled bool
}

func (f *testRevoker) RevokeAllSessions(ctx context.Context, userID string) error {
	f.revokeCalled = true
	f.revokedID = userID
	return f.revokeErr
}

func TestUploadAvatar_PNGSignatureIgnoresExtension(t *testing.T) {
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	handler := NewHandler(NewService(repo, store, mailer, crypto, sessions, cfg))

	rec := performAvatarUpload(t, handler, pngAvatarBytes(128), "avatar.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp avatarResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AvatarURL == "" {
		t.Fatal("expected avatar_url in response")
	}
	if !strings.HasPrefix(repo.avatarKeyValue(), "avatars/user-1/") {
		t.Fatalf("unexpected avatar key: %q", repo.avatarKeyValue())
	}
	if !store.hasObject(repo.avatarKeyValue()) {
		t.Fatalf("uploaded object %q was not stored", repo.avatarKeyValue())
	}
	if got := store.contentType(repo.avatarKeyValue()); got != "image/png" {
		t.Fatalf("expected image/png content type, got %q", got)
	}
	if got := repo.storageUsedValue(); got != int64(len(pngAvatarBytes(128))) {
		t.Fatalf("expected storage usage %d, got %d", len(pngAvatarBytes(128)), got)
	}
}

func TestUploadAvatar_NonImageReturns400(t *testing.T) {
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	handler := NewHandler(NewService(repo, store, mailer, crypto, sessions, cfg))

	rec := performAvatarUpload(t, handler, []byte("not an image"), "avatar.png")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.objectCount() != 0 {
		t.Fatalf("non-image upload must not store objects")
	}
}

func TestUploadAvatar_FileOver2MBReturns413(t *testing.T) {
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	handler := NewHandler(NewService(repo, store, mailer, crypto, sessions, cfg))

	oversized := bytes.Repeat([]byte{'x'}, int(MaxAvatarSizeBytes)+1)
	rec := performAvatarUpload(t, handler, oversized, "avatar.png")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.objectCount() != 0 {
		t.Fatalf("oversized upload must not store objects")
	}
}

func TestDetectAvatarMIME_AllowsPNGJPEGWEBP(t *testing.T) {
	cases := map[string][]byte{
		"image/png":  pngAvatarBytes(16),
		"image/jpeg": jpegAvatarBytes(),
		"image/webp": webpAvatarBytes(),
	}

	for want, data := range cases {
		if got := detectAvatarMIME(data); got != want {
			t.Fatalf("expected %s, got %q", want, got)
		}
	}
}

func TestReplaceAvatar_QuotaExceededSkipsPutObject(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	repo.storageUsed = 100
	repo.storageQuota = 110
	newData := pngAvatarBytes(16)
	sessions := &testRevoker{}

	service := NewService(repo, store, mailer, crypto, sessions, cfg)
	_, err := service.ReplaceAvatar(ctx, "user-1", bytes.NewReader(newData), int64(len(newData)), "image/png")
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}

	var appErr *apperr.AppError
	if !errors.As(err, &appErr) || appErr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("expected forbidden app error, got %v", err)
	}
	if store.objectCount() != 0 {
		t.Fatal("quota check must happen before PutObject")
	}
	if repo.avatarKeyValue() != "" {
		t.Fatalf("avatar key must not change on quota exceeded, got %q", repo.avatarKeyValue())
	}
}

func TestReplaceAvatar_DeletesOldObjectUpdatesUsageAndPresignsBeforeDB(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	oldKey := "avatars/user-1/old"
	oldData := []byte("old-avatar")
	newData := pngAvatarBytes(64)

	store.seed(oldKey, oldData, "image/png")
	repo.avatarKey = oldKey
	repo.storageUsed = int64(len(oldData))
	repo.onReplace = func() {
		if len(store.presignedKeyValues()) == 0 {
			t.Error("PresignedURL must be called before DB update")
		}
	}

	service := NewService(repo, store, mailer, crypto, sessions, cfg)
	url, err := service.ReplaceAvatar(ctx, "user-1", bytes.NewReader(newData), int64(len(newData)), "image/png")
	if err != nil {
		t.Fatalf("replace avatar: %v", err)
	}

	newKey := repo.avatarKeyValue()
	if url != "https://storage.test/"+newKey {
		t.Fatalf("unexpected avatar url: %q", url)
	}
	if !strings.HasPrefix(newKey, "avatars/user-1/") || newKey == oldKey {
		t.Fatalf("unexpected new avatar key: %q", newKey)
	}
	if store.hasObject(oldKey) {
		t.Fatal("old avatar object must be deleted after replacement")
	}
	if !store.hasObject(newKey) {
		t.Fatal("new avatar object must remain in storage")
	}
	if got := repo.storageUsedValue(); got != int64(len(newData)) {
		t.Fatalf("expected storage usage %d, got %d", len(newData), got)
	}
}

func TestReplaceAvatar_ReturnsCurrentURLWhenConcurrentRequestWins(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	currentKey := "avatars/user-1/newer"
	store.seed(currentKey, pngAvatarBytes(32), "image/png")
	repo.currentAfterReplace = currentKey

	newData := pngAvatarBytes(16)
	service := NewService(repo, store, mailer, crypto, sessions, cfg)
	url, err := service.ReplaceAvatar(ctx, "user-1", bytes.NewReader(newData), int64(len(newData)), "image/png")
	if err != nil {
		t.Fatalf("replace avatar: %v", err)
	}
	if url != "https://storage.test/"+currentKey {
		t.Fatalf("expected current avatar url, got %q", url)
	}
}

func TestDeleteAvatar_RemovesObjectClearsKeyAndUpdatesUsage(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAvatarRepo()
	store := newFakeObjectStorage()
	mailer := &fakeEmailSender{}
	crypto := newTestCryptox(t)
	cfg := defaultEmailConfig()
	sessions := &testRevoker{}
	oldKey := "avatars/user-1/old"
	oldData := pngAvatarBytes(40)

	store.seed(oldKey, oldData, "image/png")
	repo.avatarKey = oldKey
	repo.storageUsed = int64(len(oldData))

	service := NewService(repo, store, mailer, crypto, sessions, cfg)
	if err := service.DeleteAvatar(ctx, "user-1"); err != nil {
		t.Fatalf("delete avatar: %v", err)
	}
	if repo.avatarKeyValue() != "" {
		t.Fatalf("avatar key must be cleared, got %q", repo.avatarKeyValue())
	}
	if store.hasObject(oldKey) {
		t.Fatal("delete avatar must remove object from storage")
	}
	if got := repo.storageUsedValue(); got != 0 {
		t.Fatalf("expected storage usage 0, got %d", got)
	}
}

func performAvatarUpload(t *testing.T, handler *Handler, data []byte, filename string) *httptest.ResponseRecorder {
	t.Helper()
	req := multipartAvatarRequest(t, data, filename)
	rec := httptest.NewRecorder()
	middleware.ErrorMiddleware(handler.UploadAvatar).ServeHTTP(rec, req)
	return rec
}

func multipartAvatarRequest(t *testing.T, data []byte, filename string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	ctx := reqctx.PutUserID(context.Background(), "user-1")
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/api/v1/profile/me/avatar", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func pngAvatarBytes(payloadSize int) []byte {
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	return append(data, bytes.Repeat([]byte{0}, payloadSize)...)
}

func jpegAvatarBytes() []byte {
	return []byte{0xff, 0xd8, 0xff, 0xdb, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
}

func webpAvatarBytes() []byte {
	return []byte{'R', 'I', 'F', 'F', 0x24, 0, 0, 0, 'W', 'E', 'B', 'P', 'V', 'P', '8', ' '}
}

type storedAvatarObject struct {
	data        []byte
	contentType string
}

type fakeObjectStorage struct {
	mu            sync.Mutex
	objects       map[string]storedAvatarObject
	presignedKeys []string
	removeErrors  map[string]error
}

func newFakeObjectStorage() *fakeObjectStorage {
	return &fakeObjectStorage{
		objects:      make(map[string]storedAvatarObject),
		removeErrors: make(map[string]error),
	}
}

func (s *fakeObjectStorage) PutObject(_ context.Context, key string, reader io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("object size mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = storedAvatarObject{data: data, contentType: contentType}
	return nil
}

func (s *fakeObjectStorage) RemoveObject(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.removeErrors[key]; err != nil {
		return err
	}
	delete(s.objects, key)
	return nil
}

func (s *fakeObjectStorage) ObjectSize(_ context.Context, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[key]
	if !ok {
		return 0, storage.ErrObjectNotFound
	}
	return int64(len(obj.data)), nil
}

func (s *fakeObjectStorage) PresignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.presignedKeys = append(s.presignedKeys, key)
	return "https://storage.test/" + key, nil
}

func (s *fakeObjectStorage) seed(key string, data []byte, contentType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = storedAvatarObject{data: data, contentType: contentType}
}

func (s *fakeObjectStorage) hasObject(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *fakeObjectStorage) contentType(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[key].contentType
}

func (s *fakeObjectStorage) objectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

func (s *fakeObjectStorage) presignedKeyValues() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.presignedKeys...)
}

type fakeAvatarRepo struct {
	mu                  sync.Mutex
	avatarKey           string
	currentAfterReplace string
	storageUsed         int64
	storageQuota        int64
	orgID               sql.NullString
	onReplace           func()
}

func newFakeAvatarRepo() *fakeAvatarRepo {
	return &fakeAvatarRepo{
		storageQuota: 10 * 1024 * 1024 * 1024,
		orgID:        sql.NullString{String: "org-1", Valid: true},
	}
}

// ============ Avatar methods ============

func (r *fakeAvatarRepo) AvatarState(_ context.Context, _ string) (AvatarState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return AvatarState{
		OrgID:      r.orgID,
		AvatarKey:  r.avatarKey,
		UsedBytes:  r.storageUsed,
		QuotaBytes: r.storageQuota,
		HasOrg:     r.orgID.Valid,
	}, nil
}

func (r *fakeAvatarRepo) ReplaceAvatar(
	_ context.Context,
	_ string,
	expectedOldKey string,
	newKey string,
	oldSize int64,
	storageDelta int64,
) (AvatarChange, error) {
	if r.onReplace != nil {
		r.onReplace()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.avatarKey != expectedOldKey {
		return AvatarChange{}, ErrAvatarChanged
	}
	r.avatarKey = newKey
	r.addStorageUsage(storageDelta)
	return AvatarChange{OldKey: expectedOldKey, OldSize: oldSize, OrgID: r.orgID}, nil
}

func (r *fakeAvatarRepo) ClearAvatar(_ context.Context, _ string, expectedOldKey string, oldSize int64) (AvatarChange, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.avatarKey != expectedOldKey {
		return AvatarChange{}, ErrAvatarChanged
	}
	r.avatarKey = ""
	r.addStorageUsage(-oldSize)
	return AvatarChange{OldKey: expectedOldKey, OldSize: oldSize, OrgID: r.orgID}, nil
}

func (r *fakeAvatarRepo) RestoreAvatarIfEmpty(_ context.Context, _ string, oldKey string, oldSize int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.avatarKey != "" {
		return false, nil
	}
	r.avatarKey = oldKey
	r.addStorageUsage(oldSize)
	return true, nil
}

func (r *fakeAvatarRepo) AddOrgStorageUsage(_ context.Context, _ string, delta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addStorageUsage(delta)
	return nil
}

func (r *fakeAvatarRepo) CurrentAvatarKey(_ context.Context, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.currentAfterReplace != "" {
		return r.currentAfterReplace, nil
	}
	return r.avatarKey, nil
}

// ============ User methods ============

func (r *fakeAvatarRepo) FindByID(_ context.Context, _ uuid.UUID) (*User, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) FindByEmailHash(_ context.Context, _ []byte) (*User, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) Update(_ context.Context, _ *User, _ []byte, _ []byte) error {
	return nil
}

// ============ Token methods ============

func (r *fakeAvatarRepo) CreateToken(_ context.Context, _ *Token) error {
	return nil
}

func (r *fakeAvatarRepo) FindTokenByHash(_ context.Context, _ []byte) (*Token, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) MarkTokenUsed(_ context.Context, _ string) error {
	return nil
}

func (r *fakeAvatarRepo) DeleteToken(_ context.Context, _ string) error {
	return nil
}

func (r *fakeAvatarRepo) DeleteExpiredTokens(_ context.Context) (int64, error) {
	return 0, nil
}

// ============ Transaction methods ============

func (r *fakeAvatarRepo) BeginTx(_ context.Context) (pgx.Tx, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) FindByIDWithTx(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*User, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) FindByEmailHashWithTx(_ context.Context, _ pgx.Tx, _ []byte) (*User, error) {
	return nil, nil
}

func (r *fakeAvatarRepo) UpdateEmailWithTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ []byte, _ []byte, _ bool) error {
	return nil
}

func (r *fakeAvatarRepo) MarkTokenUsedWithTx(_ context.Context, _ pgx.Tx, _ string) error {
	return nil
}

// ============ Helper methods ============

func (r *fakeAvatarRepo) avatarKeyValue() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.avatarKey
}

func (r *fakeAvatarRepo) storageUsedValue() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.storageUsed
}

func (r *fakeAvatarRepo) addStorageUsage(delta int64) {
	r.storageUsed += delta
	if r.storageUsed < 0 {
		r.storageUsed = 0
	}
}

func (r *fakeAvatarRepo) GetUserProfile(ctx context.Context, userID uuid.UUID) (*UserProfile, error) {
	return nil, nil
}
