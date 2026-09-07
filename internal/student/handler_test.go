package student

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/internal/apperr"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureService struct {
	update     UpdateInput
	create     CreateInput
	avatar     []byte
	avatarName string
	force      bool
}

func (c *captureService) Create(_ context.Context, input CreateInput) (*Student, error) {
	c.create = input
	return &Student{ID: uuid.New()}, nil
}

func (c *captureService) List(context.Context, ListInput) (*ListResult, error) {
	return &ListResult{}, nil
}

func (c *captureService) Update(_ context.Context, _ uuid.UUID, input UpdateInput) (*Student, error) {
	c.update = input
	return &Student{ID: uuid.New()}, nil
}

func (c *captureService) ReplaceAvatar(
	_ context.Context, _ uuid.UUID, data []byte, name string,
) (*Student, error) {
	c.avatar = data
	c.avatarName = name
	mediaID, url := uuid.New(), "https://minio.test/"+name
	return &Student{ID: uuid.New(), AvatarMediaID: &mediaID, AvatarURL: &url}, nil
}

func (c *captureService) Delete(context.Context, uuid.UUID) error { return nil }

func (c *captureService) ForceDelete(_ context.Context, _ uuid.UUID) error {
	c.force = true
	return nil
}

func patchStudent(t *testing.T, body string) (*captureService, *httptest.ResponseRecorder) {
	t.Helper()
	service := &captureService{}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/students/"+uuid.New().String(), strings.NewReader(body))
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()
	require.NoError(t, NewHandler(service).Update(rec, req))
	return service, rec
}

// TestUpdateDistinguishesMissingAvatarFromNull — договорённость с фронтом:
// отсутствие поля не трогает аватар, явный null его снимает.
func TestUpdateDistinguishesMissingAvatarFromNull(t *testing.T) {
	t.Run("поле не передано", func(t *testing.T) {
		service, _ := patchStudent(t, `{"name":"Аня"}`)
		assert.False(t, service.update.AvatarMediaID.Set, "аватар не должен трогаться")
	})

	t.Run("передан null", func(t *testing.T) {
		service, _ := patchStudent(t, `{"avatar_media_id":null}`)
		assert.True(t, service.update.AvatarMediaID.Set)
		assert.Nil(t, service.update.AvatarMediaID.Value, "null снимает аватар")
	})

	t.Run("передан идентификатор", func(t *testing.T) {
		mediaID := uuid.New()
		service, _ := patchStudent(t, `{"avatar_media_id":"`+mediaID.String()+`"}`)
		require.True(t, service.update.AvatarMediaID.Set)
		require.NotNil(t, service.update.AvatarMediaID.Value)
		assert.Equal(t, mediaID, *service.update.AvatarMediaID.Value)
	})
}

func TestCreateAcceptsAvatarMediaID(t *testing.T) {
	service := &captureService{}
	mediaID := uuid.New()
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/api/v1/students",
		strings.NewReader(`{"email":"a@b.c","name":"Аня","avatar_media_id":"`+mediaID.String()+`"}`),
	)
	rec := httptest.NewRecorder()
	require.NoError(t, NewHandler(service).Create(rec, req))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, service.create.AvatarMediaID)
	assert.Equal(t, mediaID, *service.create.AvatarMediaID)
}

func TestUpdateRejectsMalformedAvatarMediaID(t *testing.T) {
	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPatch, "/api/v1/students/x",
		strings.NewReader(`{"avatar_media_id":"not-a-uuid"}`),
	)
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()

	err := NewHandler(service).Update(rec, req)
	require.Error(t, err, "битый uuid не должен доезжать до сервиса")
}

// TestUpdateDistinguishesMissingCardsShiftFromNull — та же договорённость,
// что и по аватару: отсутствие поля не трогает раскладку, null сбрасывает
// её к значению по умолчанию.
func TestUpdateDistinguishesMissingCardsShiftFromNull(t *testing.T) {
	t.Run("поле не передано", func(t *testing.T) {
		service, _ := patchStudent(t, `{"name":"Аня"}`)
		assert.False(t, service.update.CardsShift.Set, "раскладка не должна трогаться")
	})

	t.Run("передан null", func(t *testing.T) {
		service, _ := patchStudent(t, `{"cards_shift":null}`)
		assert.True(t, service.update.CardsShift.Set)
		assert.Nil(t, service.update.CardsShift.Value, "null — сброс к значению по умолчанию")
	})

	t.Run("передано значение", func(t *testing.T) {
		service, _ := patchStudent(t, `{"cards_shift":"left"}`)
		require.True(t, service.update.CardsShift.Set)
		require.NotNil(t, service.update.CardsShift.Value)
		assert.Equal(t, "left", *service.update.CardsShift.Value)
	})
}

func TestCreateAcceptsCardsShift(t *testing.T) {
	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPost, "/api/v1/students",
		strings.NewReader(`{"email":"a@b.c","name":"Аня","cards_shift":"right"}`),
	)
	rec := httptest.NewRecorder()
	require.NoError(t, NewHandler(service).Create(rec, req))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, service.create.CardsShift)
	assert.Equal(t, "right", *service.create.CardsShift)
}

// TestUpdateRejectsNonStringCardsShift: тип проверяется ещё на разборе тела,
// до сервиса.
func TestUpdateRejectsNonStringCardsShift(t *testing.T) {
	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPatch, "/api/v1/students/x",
		strings.NewReader(`{"cards_shift":1}`),
	)
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()

	require.Error(t, NewHandler(service).Update(rec, req))
}

func TestUploadAvatarReadsMultipartFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("file", "photo.png")
	require.NoError(t, err)
	_, err = file.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPut,
		"/api/v1/students/"+uuid.New().String()+"/avatar", body,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()

	require.NoError(t, NewHandler(service).UploadAvatar(rec, req))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "photo.png", service.avatarName)
	assert.NotEmpty(t, service.avatar)
	assert.Contains(t, rec.Body.String(), "avatar_url")
}

func TestUploadAvatarRequiresFileField(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("other", "x"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPut,
		"/api/v1/students/"+uuid.New().String()+"/avatar", body,
	)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("id", uuid.New().String())
	rec := httptest.NewRecorder()

	require.Error(t, NewHandler(&captureService{}).UploadAvatar(rec, req))
}

func deleteStudent(t *testing.T, query string) (*captureService, error) {
	t.Helper()
	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodDelete,
		"/api/v1/students/"+uuid.New().String()+query, nil,
	)
	req.SetPathValue("id", uuid.New().String())
	return service, NewHandler(service).Delete(httptest.NewRecorder(), req)
}

func TestDeleteReadsForceFlag(t *testing.T) {
	t.Run("без параметра — архивация", func(t *testing.T) {
		service, err := deleteStudent(t, "")
		require.NoError(t, err)
		assert.False(t, service.force)
	})

	t.Run("force=true — полное удаление", func(t *testing.T) {
		service, err := deleteStudent(t, "?force=true")
		require.NoError(t, err)
		assert.True(t, service.force)
	})

	t.Run("не булево значение — 400", func(t *testing.T) {
		_, err := deleteStudent(t, "?force=maybe")
		require.Error(t, err)
	})
}

// TestUpdateExplainsReadOnlyAvatarURL: фронт по привычке шлёт avatar_url,
// хотя ссылка presigned и только читается. Ответ должен подсказывать, чем
// её заменить, а не отдавать безымянный 400.
func TestUpdateExplainsReadOnlyAvatarURL(t *testing.T) {
	service := &captureService{}
	req := httptest.NewRequestWithContext(
		context.Background(), http.MethodPatch, "/api/v1/students/x",
		strings.NewReader(`{"avatar_url":"https://minio.test/a.png"}`),
	)
	req.SetPathValue("id", uuid.New().String())

	err := NewHandler(service).Update(httptest.NewRecorder(), req)

	require.Error(t, err)
	var appErr *apperr.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusBadRequest, appErr.HTTPStatus)
	assert.Contains(t, appErr.Message, "PUT /students/{id}/avatar")
	assert.Contains(t, appErr.Message, "avatar_media_id")
}
