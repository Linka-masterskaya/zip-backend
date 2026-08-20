package linka

import (
	"errors"
	"fmt"
	"math"
)

// Linka Looks 3.2.10 читает собственный формат набора версии 3.0
// (`pages[].cards[]`), который не совпадает с Linka Config 2.0
// (`metadata`/`settings`/`blocks[].elements[]`), несмотря на похожие
// строки версий. Клиент не падает на чужом config.json: он молча
// нормализует его в одну пустую страницу, поэтому конвертация обязана
// быть явной. Детали и матрица потерь:
// docs/compatibility/linka-looks/ADR-001-linka-looks-3.2.10.md
const LooksSetVersion = "3.0"

// Режимы страницы Linka Looks. Значения вне этого списка клиент
// заменяет на standard.
const (
	LooksModeStandard = "standard"
	LooksModeQuiz     = "quiz"
	LooksModeMatch    = "match"
)

// Типы карточек Linka Looks. Наполненная карточка всегда 0; 2 —
// пустая заготовка, которой добивается сетка страницы.
const (
	LooksCardTypeContent = 0
	LooksCardTypeEmpty   = 2
)

const (
	looksDefaultColumns = 3
	looksDefaultRows    = 3
	looksMatchRows      = 2
)

// ErrLooksUnsupportedBlock возвращается для типов заданий, которые
// Linka Looks 3.0 не умеет представлять. Молча деградировать такой
// блок нельзя: пользователь получил бы набор без задания и не узнал
// бы об этом.
var ErrLooksUnsupportedBlock = errors.New("block type is not representable in Linka Looks 3.0")

// ErrLooksUnrepresentableMatching возвращается, когда граф пар не
// раскладывается на две линейки Looks: элемент участвует в нескольких
// парах либо остаётся вне пар.
var ErrLooksUnrepresentableMatching = errors.New("matching block is not representable as Linka Looks lanes")

// ErrLooksMissingMediaPath возвращается, если у медиа-элемента нет
// пути внутри архива. Такой набор открылся бы в Looks без картинки
// или звука.
var ErrLooksMissingMediaPath = errors.New("media element has no archive path")

// LooksConfig — корень config.json формата Linka Looks 3.0.
type LooksConfig struct {
	Version     string      `json:"version"`
	Columns     int         `json:"columns"`
	Rows        int         `json:"rows"`
	Description string      `json:"description,omitempty"`
	Pages       []LooksPage `json:"pages"`
}

// LooksPage — одна страница набора. Для match-режима клиент сам
// приводит rows к 2 и распределяет карточки по линейкам, исходя из
// topColumns.
type LooksPage struct {
	ID            string      `json:"id"`
	Mode          string      `json:"mode"`
	Columns       int         `json:"columns"`
	Rows          int         `json:"rows"`
	TopColumns    *int        `json:"topColumns,omitempty"`
	BottomColumns *int        `json:"bottomColumns,omitempty"`
	Question      *string     `json:"question,omitempty"`
	Cards         []LooksCard `json:"cards"`
}

// LooksCard — карточка Looks. Одному элементу Linka Config 2.0
// соответствует ровно одна карточка.
type LooksCard struct {
	ID        string `json:"id"`
	CardType  int    `json:"cardType"`
	Title     string `json:"title,omitempty"`
	ImagePath string `json:"imagePath,omitempty"`
	AudioPath string `json:"audioPath,omitempty"`
	Answer    *bool  `json:"answer,omitempty"`
	MatchID   string `json:"matchId,omitempty"`
}

// UnsupportedBlockError называет конкретный блок, из-за которого
// экспорт в Looks невозможен, чтобы пользователь понял, что править.
type UnsupportedBlockError struct {
	BlockID   string
	BlockType string
}

func (e *UnsupportedBlockError) Error() string {
	return fmt.Sprintf("block %s of type %q: %s", e.BlockID, e.BlockType, ErrLooksUnsupportedBlock.Error())
}

func (e *UnsupportedBlockError) Unwrap() error { return ErrLooksUnsupportedBlock }

// ToLooks переводит Linka Config 2.0 в формат Linka Looks 3.0.
//
// Конвертация выполняется после того, как пути медиа переписаны на
// относительные пути внутри архива: Looks знает только про пути и не
// понимает media_id.
//
// Неподдерживаемые типы заданий (multi_choice, categories, sequence)
// и непредставимые графы пар возвращают ошибку вместо частичного
// результата.
func ToLooks(cfg *Config) (*LooksConfig, error) {
	if cfg == nil {
		return nil, errors.New("convert to Linka Looks: config is nil")
	}
	columns := positiveOr(cfg.Settings.Columns, looksDefaultColumns)
	rows := positiveOr(cfg.Settings.Rows, looksDefaultRows)

	out := &LooksConfig{
		Version:     LooksSetVersion,
		Columns:     columns,
		Rows:        rows,
		Description: cfg.Metadata.Title,
		Pages:       make([]LooksPage, 0, len(cfg.Blocks)),
	}
	for i := range cfg.Blocks {
		page, err := blockToPage(&cfg.Blocks[i], columns, rows)
		if err != nil {
			return nil, err
		}
		out.Pages = append(out.Pages, *page)
	}
	return out, nil
}

func blockToPage(block *Block, columns, rows int) (*LooksPage, error) {
	switch block.Type {
	case BlockTypeGrid:
		return standardPage(block, LooksModeStandard, columns, rows)
	case BlockTypeSingleChoice:
		return quizPage(block, columns, rows)
	case BlockTypeMatching:
		return matchPage(block)
	case BlockTypeMultiChoice, BlockTypeCategories, BlockTypeSequence:
		return nil, &UnsupportedBlockError{BlockID: block.ID, BlockType: block.Type}
	default:
		return nil, &UnsupportedBlockError{BlockID: block.ID, BlockType: block.Type}
	}
}

func standardPage(block *Block, mode string, columns, rows int) (*LooksPage, error) {
	cards, err := elementCards(block.Elements)
	if err != nil {
		return nil, err
	}
	pageRows := fitRows(len(cards), columns, rows)
	return &LooksPage{
		ID:      block.ID,
		Mode:    mode,
		Columns: columns,
		Rows:    pageRows,
		Cards:   padCards(cards, columns*pageRows),
	}, nil
}

func quizPage(block *Block, columns, rows int) (*LooksPage, error) {
	page, err := standardPage(block, LooksModeQuiz, columns, rows)
	if err != nil {
		return nil, err
	}
	correct := make(map[string]bool, len(block.Answers))
	for _, answer := range block.Answers {
		if answer.IsCorrect {
			correct[answer.ElementID] = true
		}
	}
	for i := range page.Cards {
		if correct[page.Cards[i].ID] {
			answer := true
			page.Cards[i].Answer = &answer
		}
	}
	// Looks показывает вопрос над карточками. В Linka Config 2.0
	// источника для него нет, поэтому оставляем пустую строку —
	// клиент всё равно подставит её при нормализации.
	question := ""
	page.Question = &question
	return page, nil
}

// matchPage раскладывает пары на две линейки Looks: левые элементы
// сверху, правые снизу, общий matchId связывает карточки пары.
func matchPage(block *Block) (*LooksPage, error) {
	if len(block.Pairs) == 0 {
		return nil, fmt.Errorf("%w: block %s has no pairs", ErrLooksUnrepresentableMatching, block.ID)
	}
	elements := make(map[string]*Element, len(block.Elements))
	for i := range block.Elements {
		elements[block.Elements[i].ID] = &block.Elements[i]
	}

	used := make(map[string]struct{}, len(block.Elements))
	top := make([]LooksCard, 0, len(block.Pairs))
	bottom := make([]LooksCard, 0, len(block.Pairs))
	for index, pair := range block.Pairs {
		matchID := fmt.Sprintf("%s-%d", block.ID, index+1)
		left, err := pairCard(block, elements, used, pair.LeftID, matchID)
		if err != nil {
			return nil, err
		}
		right, err := pairCard(block, elements, used, pair.RightID, matchID)
		if err != nil {
			return nil, err
		}
		top = append(top, *left)
		bottom = append(bottom, *right)
	}
	// Элемент вне пар потерялся бы при открытии: Looks кладёт в
	// линейки только то, что описано парами.
	for i := range block.Elements {
		if _, ok := used[block.Elements[i].ID]; !ok {
			return nil, fmt.Errorf(
				"%w: element %s of block %s is not part of any pair",
				ErrLooksUnrepresentableMatching, block.Elements[i].ID, block.ID,
			)
		}
	}

	lane := len(block.Pairs)
	return &LooksPage{
		ID:            block.ID,
		Mode:          LooksModeMatch,
		Columns:       lane,
		Rows:          looksMatchRows,
		TopColumns:    &lane,
		BottomColumns: &lane,
		Cards:         append(top, bottom...),
	}, nil
}

func pairCard(
	block *Block,
	elements map[string]*Element,
	used map[string]struct{},
	elementID string,
	matchID string,
) (*LooksCard, error) {
	element, ok := elements[elementID]
	if !ok {
		return nil, fmt.Errorf(
			"%w: block %s references unknown element %s",
			ErrLooksUnrepresentableMatching, block.ID, elementID,
		)
	}
	if _, duplicate := used[elementID]; duplicate {
		return nil, fmt.Errorf(
			"%w: element %s of block %s belongs to more than one pair",
			ErrLooksUnrepresentableMatching, elementID, block.ID,
		)
	}
	used[elementID] = struct{}{}

	card, err := elementCard(element)
	if err != nil {
		return nil, err
	}
	card.MatchID = matchID
	return card, nil
}

func elementCards(elements []Element) ([]LooksCard, error) {
	cards := make([]LooksCard, 0, len(elements))
	for i := range elements {
		card, err := elementCard(&elements[i])
		if err != nil {
			return nil, err
		}
		cards = append(cards, *card)
	}
	return cards, nil
}

func elementCard(element *Element) (*LooksCard, error) {
	card := LooksCard{ID: element.ID, CardType: LooksCardTypeContent}
	switch element.Kind {
	case ElementKindText:
		card.Title = element.Value
	case ElementKindImage:
		if element.MediaURL == "" {
			return nil, fmt.Errorf("%w: element %s", ErrLooksMissingMediaPath, element.ID)
		}
		card.ImagePath = element.MediaURL
	case ElementKindAudio:
		if element.MediaURL == "" {
			return nil, fmt.Errorf("%w: element %s", ErrLooksMissingMediaPath, element.ID)
		}
		card.AudioPath = element.MediaURL
	default:
		return nil, fmt.Errorf("element %s has unknown kind %q", element.ID, element.Kind)
	}
	return &card, nil
}

// padCards добивает страницу пустыми карточками до размера сетки.
// Без этого Looks добавит заготовки сам, но со своими случайными
// идентификаторами.
func padCards(cards []LooksCard, size int) []LooksCard {
	for len(cards) < size {
		cards = append(cards, LooksCard{
			ID:       fmt.Sprintf("empty-%d", len(cards)+1),
			CardType: LooksCardTypeEmpty,
		})
	}
	return cards
}

// fitRows подбирает число строк так, чтобы поместились все карточки.
// Looks обрезает страницу до rows*columns, поэтому заниженная сетка
// молча выбросила бы часть набора.
func fitRows(cardCount, columns, rows int) int {
	if cardCount == 0 {
		return rows
	}
	needed := int(math.Ceil(float64(cardCount) / float64(columns)))
	if needed > rows {
		return needed
	}
	return rows
}

func positiveOr(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// looks3Converter — стратегия экспорта в формат Linka Looks 3.0.
type looks3Converter struct{}

func (looks3Converter) Format() Format { return FormatLooks3 }

func (looks3Converter) Description() string {
	return "формат набора Linka Looks 3.0 (pages[].cards[]); " +
		"multi_choice, categories и sequence не поддерживаются"
}

func (looks3Converter) Convert(cfg *Config) (any, error) {
	looks, err := ToLooks(cfg)
	if err != nil {
		// Возвращаем nil явно: типизированный nil-указатель в any
		// перестал бы быть nil для вызывающего кода.
		return nil, err
	}
	return looks, nil
}

func init() { Register(looks3Converter{}) }
