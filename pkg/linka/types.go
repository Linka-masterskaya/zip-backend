package linka

import (
	"encoding/json"

	"github.com/google/uuid"
)

const (
	BlockTypeGrid         = "grid"
	BlockTypeSingleChoice = "single_choice"
	BlockTypeMultiChoice  = "multi_choice"
	BlockTypeMatching     = "matching"
	BlockTypeCategories   = "categories"
	BlockTypeSequence     = "sequence"
)

// Вид карточки. Текст, картинка и озвучка — не виды, а атрибуты: у
// обычной карточки они могут быть все сразу, как у карточки Linka Looks.
const (
	ElementKindNormal = "normal"
	ElementKindText   = "text"
	ElementKindEmpty  = "empty"
	ElementKindSpace  = "space"
)

// Устаревшие виды: до появления составной карточки один элемент нёс
// ровно одно вложение. Принимаются на чтение и приводятся к составной
// форме, наружу не отдаются.
const (
	legacyKindImage = "image"
	legacyKindAudio = "audio"
)

type Config struct {
	Metadata Metadata `json:"metadata"`
	Settings Settings `json:"settings"`
	Blocks   []Block  `json:"blocks"`
}

type Metadata struct {
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}

// Settings — сетка по умолчанию для блоков без собственного Layout.
type Settings struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// Layout — прямоугольная сетка блока.
type Layout struct {
	Rows    int `json:"rows"`
	Columns int `json:"columns"`
}

// Capacity — сколько элементов помещается в сетку.
func (l Layout) Capacity() int { return l.Rows * l.Columns }

// EffectiveLayout — сетка блока либо, если своей нет, сетка набора.
func (b Block) EffectiveLayout(defaults Settings) Layout {
	if b.Layout != nil {
		return *b.Layout
	}
	return Layout{Rows: defaults.Rows, Columns: defaults.Columns}
}

type Block struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
	// Layout — сетка этого блока. У каждого задания в наборе своя
	// раскладка, как и у страницы в Linka Looks. Опциональна: у наборов,
	// сохранённых до её появления, действует Settings.
	Layout   *Layout   `json:"layout,omitempty"`
	Elements []Element `json:"elements"`

	Answers  []Answer   `json:"answers,omitempty"`
	Pairs    []Pair     `json:"pairs,omitempty"`
	Category []Category `json:"categories,omitempty"`
	Sequence []SeqItem  `json:"sequence,omitempty"`
}

// Element — карточка. Kind задаёт вид, остальное — содержимое, и у
// обычной карточки его может быть три вида сразу.
type Element struct {
	ID    string        `json:"id"`
	Kind  string        `json:"kind"`
	Text  string        `json:"text,omitempty"`
	Image *ElementImage `json:"image,omitempty"`
	Audio *ElementAudio `json:"audio,omitempty"`
}

// ElementImage — картинка карточки: из медиа пользователя либо из банка
// картинок. MediaURL — путь внутри .linka, проставляется при экспорте.
type ElementImage struct {
	MediaID         *uuid.UUID `json:"media_id,omitempty"`
	MediaURL        string     `json:"media_url,omitempty"`
	SourcePictureID *uuid.UUID `json:"source_picture_id,omitempty"`
}

// ElementAudio — озвучка карточки. Text — исходный текст для TTS, чтобы
// озвучку можно было пересобрать другим голосом.
type ElementAudio struct {
	MediaID  *uuid.UUID `json:"media_id,omitempty"`
	MediaURL string     `json:"media_url,omitempty"`
	Text     string     `json:"text,omitempty"`
}

// legacyElement — форма до составной карточки: kind text|image|audio
// и одно вложение в плоских полях.
type legacyElement struct {
	ID              string        `json:"id"`
	Kind            string        `json:"kind"`
	Text            string        `json:"text,omitempty"`
	Image           *ElementImage `json:"image,omitempty"`
	Audio           *ElementAudio `json:"audio,omitempty"`
	Value           string        `json:"value,omitempty"`
	MediaID         *uuid.UUID    `json:"media_id,omitempty"`
	MediaURL        string        `json:"media_url,omitempty"`
	SourcePictureID *uuid.UUID    `json:"source_picture_id,omitempty"`
}

// UnmarshalJSON принимает и составную, и устаревшую форму. Устаревшая
// приводится к составной сразу при чтении, поэтому остальной код видит
// один формат.
func (e *Element) UnmarshalJSON(data []byte) error {
	var raw legacyElement
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = Element{ID: raw.ID, Kind: raw.Kind, Text: raw.Text, Image: raw.Image, Audio: raw.Audio}
	switch raw.Kind {
	case ElementKindText:
		if e.Text == "" {
			e.Text = raw.Value
		}
	case legacyKindImage:
		e.Kind = ElementKindNormal
		if e.Text == "" {
			e.Text = raw.Value
		}
		if e.Image == nil {
			e.Image = &ElementImage{
				MediaID: raw.MediaID, MediaURL: raw.MediaURL, SourcePictureID: raw.SourcePictureID,
			}
		}
	case legacyKindAudio:
		e.Kind = ElementKindNormal
		if e.Text == "" {
			e.Text = raw.Value
		}
		if e.Audio == nil {
			e.Audio = &ElementAudio{MediaID: raw.MediaID, MediaURL: raw.MediaURL}
		}
	}
	return nil
}

// MediaIDs — идентификаторы медиа-файлов карточки. Порядок: картинка,
// затем озвучка.
func (e Element) MediaIDs() []uuid.UUID {
	ids := make([]uuid.UUID, 0, 2)
	if e.Image != nil && e.Image.MediaID != nil && *e.Image.MediaID != uuid.Nil {
		ids = append(ids, *e.Image.MediaID)
	}
	if e.Audio != nil && e.Audio.MediaID != nil && *e.Audio.MediaID != uuid.Nil {
		ids = append(ids, *e.Audio.MediaID)
	}
	return ids
}

type Answer struct {
	ElementID string `json:"element_id"`
	IsCorrect bool   `json:"is_correct"`
}

type Pair struct {
	LeftID  string `json:"left_id"`
	RightID string `json:"right_id"`
}

type Category struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Items []string `json:"items"`
}

type SeqItem struct {
	ElementID string `json:"element_id"`
	Order     int    `json:"order"`
}
