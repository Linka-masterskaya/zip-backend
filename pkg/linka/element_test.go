package linka_test

import (
	"encoding/json"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
	"github.com/google/uuid"
)

// TestElementLegacyKindsNormalize — старая форма элемента (kind:
// text|image|audio с одним media_id) читается и приводится к составной:
// kind описывает вид карточки, а текст, картинка и озвучка — её
// атрибуты. Уже сохранённые наборы должны открываться без миграции.
func TestElementLegacyKindsNormalize(t *testing.T) {
	mediaID := uuid.New()
	pictureID := uuid.New()

	tests := []struct {
		name string
		raw  string
		want linka.Element
	}{
		{
			"legacy text",
			`{"id":"e","kind":"text","value":"Кошка"}`,
			linka.Element{ID: "e", Kind: linka.ElementKindText, Text: "Кошка"},
		},
		{
			"legacy image with caption",
			`{"id":"e","kind":"image","value":"Кошка","media_id":"` + mediaID.String() + `"}`,
			linka.Element{ID: "e", Kind: linka.ElementKindNormal, Text: "Кошка",
				Image: &linka.ElementImage{MediaID: &mediaID}},
		},
		{
			"legacy image from pictures bank",
			`{"id":"e","kind":"image","source_picture_id":"` + pictureID.String() + `"}`,
			linka.Element{ID: "e", Kind: linka.ElementKindNormal,
				Image: &linka.ElementImage{SourcePictureID: &pictureID}},
		},
		{
			"legacy audio",
			`{"id":"e","kind":"audio","media_id":"` + mediaID.String() + `","media_url":"media/a.wav"}`,
			linka.Element{ID: "e", Kind: linka.ElementKindNormal,
				Audio: &linka.ElementAudio{MediaID: &mediaID, MediaURL: "media/a.wav"}},
		},
		{
			"composite passes through",
			`{"id":"e","kind":"normal","text":"Кошка","image":{"media_id":"` + mediaID.String() + `"},"audio":{"media_id":"` + pictureID.String() + `","text":"рыжий кот"}}`,
			linka.Element{ID: "e", Kind: linka.ElementKindNormal, Text: "Кошка",
				Image: &linka.ElementImage{MediaID: &mediaID},
				Audio: &linka.ElementAudio{MediaID: &pictureID, Text: "рыжий кот"}},
		},
		{
			"empty and space carry nothing",
			`{"id":"e","kind":"space"}`,
			linka.Element{ID: "e", Kind: linka.ElementKindSpace},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got linka.Element
			if err := json.Unmarshal([]byte(tt.raw), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			assertElementEqual(t, tt.want, got)
		})
	}
}

// TestElementMarshalsCanonicalForm — наружу всегда уходит составная
// форма, без legacy-полей: клиенты видят один формат.
func TestElementMarshalsCanonicalForm(t *testing.T) {
	mediaID := uuid.New()
	var el linka.Element
	if err := json.Unmarshal([]byte(`{"id":"e","kind":"image","value":"Кошка","media_id":"`+mediaID.String()+`"}`), &el); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(el)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]any
	if err = json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back["kind"] != "normal" || back["text"] != "Кошка" {
		t.Errorf("canonical kind/text lost: %s", out)
	}
	if _, legacy := back["value"]; legacy {
		t.Errorf("legacy field value must not be emitted: %s", out)
	}
	if _, legacy := back["media_id"]; legacy {
		t.Errorf("legacy field media_id must not be emitted: %s", out)
	}
	image, ok := back["image"].(map[string]any)
	if !ok || image["media_id"] != mediaID.String() {
		t.Errorf("image must be nested: %s", out)
	}
}

func assertElementEqual(t *testing.T, want, got linka.Element) {
	t.Helper()
	if want.ID != got.ID || want.Kind != got.Kind || want.Text != got.Text {
		t.Fatalf("id/kind/text: want %+v, got %+v", want, got)
	}
	if (want.Image == nil) != (got.Image == nil) {
		t.Fatalf("image presence: want %v, got %v", want.Image != nil, got.Image != nil)
	}
	if want.Image != nil && (!uuidPtrEqual(want.Image.MediaID, got.Image.MediaID) ||
		!uuidPtrEqual(want.Image.SourcePictureID, got.Image.SourcePictureID) ||
		want.Image.MediaURL != got.Image.MediaURL) {
		t.Fatalf("image: want %+v, got %+v", *want.Image, *got.Image)
	}
	if (want.Audio == nil) != (got.Audio == nil) {
		t.Fatalf("audio presence: want %v, got %v", want.Audio != nil, got.Audio != nil)
	}
	if want.Audio != nil && (!uuidPtrEqual(want.Audio.MediaID, got.Audio.MediaID) ||
		want.Audio.MediaURL != got.Audio.MediaURL || want.Audio.Text != got.Audio.Text) {
		t.Fatalf("audio: want %+v, got %+v", *want.Audio, *got.Audio)
	}
}

func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
