package linka

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Linka Looks 3.2.10 читает собственный формат набора версии 3.0
// (`pages[].cards[]`), который не совпадает с Linka Config 2.0
// (`metadata`/`settings`/`blocks[].elements[]`), несмотря на похожие
// строки версий. Клиент не падает на чужом config.json: он молча
// нормализует его в одну пустую страницу, поэтому конвертация обязана
// быть явной. Детали и матрица потерь:
// docs/compatibility/linka-looks/ADR-001-linka-looks-3.2.10.md.
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
	LooksCardTypeSpace   = 1
	LooksCardTypeEmpty   = 2
)

const (
	looksDefaultColumns = 3
	looksDefaultRows    = 3
	looksMatchRows      = 2
)

// ErrLooksUnsupportedBlock возвращается для типов заданий, которые
// Linka Looks 3.0 не умеет представлять. Молча выкидывать такой блок
// нельзя: пользователь получил бы набор без задания и не узнал бы об
// этом.
var ErrLooksUnsupportedBlock = errors.New("block type is not representable in Linka Looks 3.0")

// ErrLooksUnrepresentableMatching возвращается, когда граф пар не
// раскладывается на две линейки Looks: элемент участвует в нескольких
// парах либо остаётся вне пар.
var ErrLooksUnrepresentableMatching = errors.New("matching block is not representable as Linka Looks lanes")

// ErrLooksMissingMediaPath возвращается, если у медиа-элемента нет
// пути внутри архива. Такой набор открылся бы в Looks без картинки
// или звука.
// ErrLooksUnrepresentableCategories возвращается, когда распределение
// не раскладывается на match-группы Looks: элемент вне категорий или
// сразу в нескольких, либо категория без элементов.
var ErrLooksUnrepresentableCategories = errors.New("categories block is not representable as Linka Looks match groups")

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
// В Looks всего три режима, поэтому часть заданий переводится с
// потерями, согласованными с заказчиком:
//   - multi_choice → quiz: все верные помечены, засчитывается первый
//     верный выбор;
//   - sequence → standard: карточки стоят в правильном порядке, но
//     задание не проверяется;
//   - categories → match-группы: ребёнок соединяет категорию с каждым
//     её элементом вместо раскладывания по колонкам.
//
// Непредставимые пары и распределения возвращают ошибку вместо
// частичного результата.
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
		// Сетка страницы — из блока; у наборов без layout действует
		// сетка набора.
		layout := cfg.Blocks[i].EffectiveLayout(cfg.Settings)
		page, err := blockToPage(
			&cfg.Blocks[i],
			positiveOr(layout.Columns, columns),
			positiveOr(layout.Rows, rows),
		)
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
	case BlockTypeSingleChoice, BlockTypeMultiChoice:
		return quizPage(block, columns, rows)
	case BlockTypeMatching:
		return matchPage(block)
	case BlockTypeSequence:
		return sequencePage(block, columns, rows)
	case BlockTypeCategories:
		return categoriesPage(block)
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

// sequencePage ставит карточки в правильном порядке на обычную
// страницу. Режима «порядок» в Looks нет, поэтому задание видно, но не
// проверяется. Элементы вне sequence идут следом в исходном порядке.
func sequencePage(block *Block, columns, rows int) (*LooksPage, error) {
	order := make(map[string]int, len(block.Sequence))
	for _, item := range block.Sequence {
		order[item.ElementID] = item.Order
	}
	elements := append([]Element(nil), block.Elements...)
	sort.SliceStable(elements, func(i, j int) bool {
		left, leftOK := order[elements[i].ID]
		right, rightOK := order[elements[j].ID]
		if leftOK != rightOK {
			return leftOK
		}
		return leftOK && left < right
	})
	ordered := *block
	ordered.Elements = elements
	return standardPage(&ordered, LooksModeStandard, columns, rows)
}

// categoriesPage раскладывает распределение на две линейки match:
// заголовки категорий сверху, элементы снизу. Looks считает карточки с
// общим matchId группой и требует соединить каждую верхнюю со всеми
// нижними той же группы, поэтому дублировать заголовок не нужно.
func categoriesPage(block *Block) (*LooksPage, error) {
	if len(block.Category) == 0 {
		return nil, fmt.Errorf("%w: block %s has no categories", ErrLooksUnrepresentableCategories, block.ID)
	}
	elements := make(map[string]*Element, len(block.Elements))
	for i := range block.Elements {
		elements[block.Elements[i].ID] = &block.Elements[i]
	}

	used := make(map[string]struct{}, len(block.Elements))
	top := make([]LooksCard, 0, len(block.Category))
	bottom := make([]LooksCard, 0, len(block.Elements))
	for _, category := range block.Category {
		if len(category.Items) == 0 {
			return nil, fmt.Errorf("%w: category %s of block %s has no items",
				ErrLooksUnrepresentableCategories, category.ID, block.ID)
		}
		header, err := categoryHeaderCard(block, elements, used, category)
		if err != nil {
			return nil, err
		}
		top = append(top, *header)
		for _, itemID := range category.Items {
			element, ok := elements[itemID]
			if !ok {
				return nil, fmt.Errorf("%w: block %s references unknown element %s",
					ErrLooksUnrepresentableCategories, block.ID, itemID)
			}
			if _, duplicate := used[itemID]; duplicate {
				return nil, fmt.Errorf("%w: element %s of block %s belongs to more than one category",
					ErrLooksUnrepresentableCategories, itemID, block.ID)
			}
			used[itemID] = struct{}{}
			card, err := elementCard(element)
			if err != nil {
				return nil, err
			}
			card.MatchID = category.ID
			bottom = append(bottom, *card)
		}
	}
	// Looks не даёт сыграть match-страницу, где у карточки с содержимым
	// нет группы. Пустые заготовки и пробелы в линейки не переносятся.
	for i := range block.Elements {
		element := &block.Elements[i]
		if _, ok := used[element.ID]; ok || element.Kind == ElementKindEmpty || element.Kind == ElementKindSpace {
			continue
		}
		return nil, fmt.Errorf("%w: element %s of block %s is not in any category",
			ErrLooksUnrepresentableCategories, element.ID, block.ID)
	}

	topColumns, bottomColumns := len(top), len(bottom)
	return &LooksPage{
		ID:            block.ID,
		Mode:          LooksModeMatch,
		Columns:       max(topColumns, bottomColumns),
		Rows:          looksMatchRows,
		TopColumns:    &topColumns,
		BottomColumns: &bottomColumns,
		Cards:         append(top, bottom...),
	}, nil
}

// categoryHeaderCard — карточка заголовка: элемент из блока либо, у
// наборов до element_id, текстовая карточка из Name.
func categoryHeaderCard(
	block *Block,
	elements map[string]*Element,
	used map[string]struct{},
	category Category,
) (*LooksCard, error) {
	if category.ElementID == "" {
		return &LooksCard{
			ID:       block.ID + "-" + category.ID,
			CardType: LooksCardTypeContent,
			Title:    category.Name,
			MatchID:  category.ID,
		}, nil
	}
	element, ok := elements[category.ElementID]
	if !ok {
		return nil, fmt.Errorf("%w: block %s references unknown element %s",
			ErrLooksUnrepresentableCategories, block.ID, category.ElementID)
	}
	used[category.ElementID] = struct{}{}
	card, err := elementCard(element)
	if err != nil {
		return nil, err
	}
	card.MatchID = category.ID
	return card, nil
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
	card := LooksCard{ID: element.ID}
	switch element.Kind {
	case ElementKindNormal, ElementKindText:
		card.CardType = LooksCardTypeContent
	case ElementKindSpace:
		card.CardType = LooksCardTypeSpace
		return &card, nil
	case ElementKindEmpty:
		card.CardType = LooksCardTypeEmpty
		return &card, nil
	default:
		return nil, fmt.Errorf("element %s has unknown kind %q", element.ID, element.Kind)
	}
	// Карточка Looks несёт подпись, картинку и озвучку одновременно —
	// так же, как наша составная карточка.
	card.Title = element.Text
	if element.Image != nil {
		if element.Image.MediaURL == "" {
			return nil, fmt.Errorf("%w: element %s image", ErrLooksMissingMediaPath, element.ID)
		}
		card.ImagePath = element.Image.MediaURL
	}
	if element.Audio != nil {
		if element.Audio.MediaURL == "" {
			return nil, fmt.Errorf("%w: element %s audio", ErrLooksMissingMediaPath, element.ID)
		}
		card.AudioPath = element.Audio.MediaURL
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
		"multi_choice — quiz с первым верным ответом, sequence — standard без проверки, " +
		"categories — match-группы"
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

// CheckLooks сообщает, выражается ли набор в Linka Looks 3.0, без
// сборки архива. Пути медиа проставляются только при экспорте, поэтому
// здесь их заменяют заглушки: проверяется структура заданий.
func CheckLooks(cfg Config) error {
	blocks := make([]Block, len(cfg.Blocks))
	for i, block := range cfg.Blocks {
		block.Elements = append([]Element(nil), block.Elements...)
		for j := range block.Elements {
			element := &block.Elements[j]
			if element.Image != nil {
				image := *element.Image
				image.MediaURL = "media"
				element.Image = &image
			}
			if element.Audio != nil {
				audio := *element.Audio
				audio.MediaURL = "media"
				element.Audio = &audio
			}
		}
		blocks[i] = block
	}
	cfg.Blocks = blocks
	_, err := ToLooks(&cfg)
	return err
}
