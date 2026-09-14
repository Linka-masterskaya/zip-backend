package linka

import "github.com/google/uuid"

const (
	BlockTypeGrid         = "grid"
	BlockTypeSingleChoice = "single_choice"
	BlockTypeMultiChoice  = "multi_choice"
	BlockTypeMatching     = "matching"
	BlockTypeCategories   = "categories"
	BlockTypeSequence     = "sequence"
)

const (
	ElementKindText  = "text"
	ElementKindImage = "image"
	ElementKindAudio = "audio"
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

type Element struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Value           string     `json:"value,omitempty"`
	MediaID         *uuid.UUID `json:"media_id,omitempty"`
	MediaURL        string     `json:"media_url,omitempty"`
	SourcePictureID *uuid.UUID `json:"source_picture_id,omitempty"`
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
