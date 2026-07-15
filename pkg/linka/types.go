package linka

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

type Settings struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

type Block struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Elements []Element `json:"elements"`

	Answers  []Answer   `json:"answers,omitempty"`
	Pairs    []Pair     `json:"pairs,omitempty"`
	Category []Category `json:"categories,omitempty"`
	Sequence []SeqItem  `json:"sequence,omitempty"`
}

type Element struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Value    string `json:"value,omitempty"`
	MediaURL string `json:"media_url,omitempty"`
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
