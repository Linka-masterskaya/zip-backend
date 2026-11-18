package linka_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Linka-masterskaya/zip-backend/pkg/linka"
)

// spikeFixture — тот же набор, на котором спайк N5 доказал
// несовместимость текущего экспорта с Linka Looks 3.2.10.
const spikeFixture = "../../docs/compatibility/linka-looks/testdata/source-config.json"

func loadSpikeConfig(t *testing.T) *linka.Config {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(spikeFixture))
	if err != nil {
		t.Fatalf("read spike fixture: %v", err)
	}
	var cfg linka.Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode spike fixture: %v", err)
	}
	// Экспорт переписывает media_id в путь внутри архива до
	// конвертации, поэтому повторяем это здесь.
	for blockIndex := range cfg.Blocks {
		for elementIndex := range cfg.Blocks[blockIndex].Elements {
			element := &cfg.Blocks[blockIndex].Elements[elementIndex]
			// Фикстура в устаревшей форме; после чтения она уже составная.
			if element.Image != nil {
				element.Image.MediaURL = "media/" + element.ID + ".png"
			}
			if element.Audio != nil {
				element.Audio.MediaURL = "media/" + element.ID + ".wav"
			}
		}
	}
	return &cfg
}

func convertSpikeFixture(t *testing.T) *linka.LooksConfig {
	t.Helper()
	got, err := linka.ToLooks(loadSpikeConfig(t))
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	if got.Version != linka.LooksSetVersion {
		t.Fatalf("version = %q, want %q", got.Version, linka.LooksSetVersion)
	}
	if len(got.Pages) != 2 {
		t.Fatalf("pages = %d, want 2 (one per block)", len(got.Pages))
	}
	return got
}

// TestToLooksConvertsSingleChoice проверяет то, что прежний экспорт
// терял: кириллицу, пути медиа и отметку правильного ответа.
func TestToLooksConvertsSingleChoice(t *testing.T) {
	quiz := convertSpikeFixture(t).Pages[0]

	if quiz.ID != "quiz-block" {
		t.Errorf("page id = %q, want quiz-block", quiz.ID)
	}
	if quiz.Mode != linka.LooksModeQuiz {
		t.Errorf("single_choice mode = %q, want %q", quiz.Mode, linka.LooksModeQuiz)
	}
	if quiz.Question == nil {
		t.Error("quiz page must carry a question field")
	}
	if quiz.Cards[0].Title != "Ёжик — правильный ответ" {
		t.Errorf("cyrillic title lost: %q", quiz.Cards[0].Title)
	}
	if quiz.Cards[1].ImagePath != "media/image-one.png" {
		t.Errorf("image path = %q", quiz.Cards[1].ImagePath)
	}
	if quiz.Cards[2].AudioPath != "media/audio-one.wav" {
		t.Errorf("audio path = %q", quiz.Cards[2].AudioPath)
	}
	if quiz.Cards[0].Answer == nil || !*quiz.Cards[0].Answer {
		t.Error("correct answer must be marked on the card")
	}
	if quiz.Cards[1].Answer != nil {
		t.Error("incorrect answer must not carry the answer flag")
	}
}

// TestToLooksConvertsMatching проверяет раскладку пар на линейки
// Looks: верхняя — левые элементы, нижняя — правые.
func TestToLooksConvertsMatching(t *testing.T) {
	match := convertSpikeFixture(t).Pages[1]

	if match.Mode != linka.LooksModeMatch {
		t.Errorf("matching mode = %q, want %q", match.Mode, linka.LooksModeMatch)
	}
	if match.TopColumns == nil || *match.TopColumns != 2 {
		t.Errorf("topColumns = %v, want 2", match.TopColumns)
	}
	if len(match.Cards) != 4 {
		t.Fatalf("match cards = %d, want 4", len(match.Cards))
	}
	if match.Cards[0].Title != "Кошка" || match.Cards[2].Title != "cat" {
		t.Errorf("lanes are out of order: %q / %q", match.Cards[0].Title, match.Cards[2].Title)
	}
	if match.Cards[0].MatchID != match.Cards[2].MatchID {
		t.Error("paired cards must share matchId")
	}
	if match.Cards[0].MatchID == match.Cards[1].MatchID {
		t.Error("different pairs must not share matchId")
	}
}

func TestToLooksFitsGridSoNoCardIsDropped(t *testing.T) {
	// Looks обрезает страницу до rows*columns. Сетка 3x1 из настроек
	// меньше пяти карточек, поэтому строк должно стать больше.
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 3, Rows: 1},
		Blocks: []linka.Block{{
			ID:   "grid-block",
			Type: linka.BlockTypeGrid,
			Elements: []linka.Element{
				{ID: "a", Kind: linka.ElementKindText, Text: "1"},
				{ID: "b", Kind: linka.ElementKindText, Text: "2"},
				{ID: "c", Kind: linka.ElementKindText, Text: "3"},
				{ID: "d", Kind: linka.ElementKindText, Text: "4"},
				{ID: "e", Kind: linka.ElementKindText, Text: "5"},
			},
		}},
	}
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	page := got.Pages[0]
	if page.Rows*page.Columns < 5 {
		t.Fatalf("grid %dx%d is too small for 5 cards", page.Columns, page.Rows)
	}
	if len(page.Cards) != page.Rows*page.Columns {
		t.Errorf("cards = %d, want %d to fill the grid", len(page.Cards), page.Rows*page.Columns)
	}
	for i := 0; i < 5; i++ {
		if page.Cards[i].CardType != linka.LooksCardTypeContent {
			t.Errorf("card %d must keep content", i)
		}
	}
	if page.Cards[5].CardType != linka.LooksCardTypeEmpty {
		t.Error("grid tail must be padded with empty cards")
	}
}

func TestToLooksRejectsUnknownBlockType(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 2, Rows: 2},
		Blocks: []linka.Block{{
			ID:       "b1",
			Type:     "puzzle",
			Elements: []linka.Element{{ID: "a", Kind: linka.ElementKindText, Text: "x"}},
		}},
	}
	_, err := linka.ToLooks(cfg)
	if !errors.Is(err, linka.ErrLooksUnsupportedBlock) {
		t.Fatalf("err = %v, want ErrLooksUnsupportedBlock", err)
	}
	var unsupported *linka.UnsupportedBlockError
	if !errors.As(err, &unsupported) || unsupported.BlockType != "puzzle" {
		t.Errorf("error must name the offending block, got %v", err)
	}
}

// TestToLooksMultiChoiceBecomesQuiz: в Looks нет множественного выбора.
// Все верные помечаются answer, засчитывается первый верный клик.
func TestToLooksMultiChoiceBecomesQuiz(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 2, Rows: 2},
		Blocks: []linka.Block{{
			ID: "multi", Type: linka.BlockTypeMultiChoice,
			Elements: []linka.Element{
				{ID: "a", Kind: linka.ElementKindText, Text: "a"},
				{ID: "b", Kind: linka.ElementKindText, Text: "b"},
				{ID: "c", Kind: linka.ElementKindText, Text: "c"},
			},
			Answers: []linka.Answer{
				{ElementID: "a", IsCorrect: true},
				{ElementID: "b", IsCorrect: false},
				{ElementID: "c", IsCorrect: true},
			},
		}},
	}
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	page := got.Pages[0]
	if page.Mode != linka.LooksModeQuiz {
		t.Fatalf("mode = %q, want quiz", page.Mode)
	}
	answers := map[string]bool{}
	for _, card := range page.Cards {
		if card.Answer != nil && *card.Answer {
			answers[card.ID] = true
		}
	}
	if len(answers) != 2 || !answers["a"] || !answers["c"] {
		t.Fatalf("answers = %v, want a and c", answers)
	}
}

// TestToLooksSequenceBecomesOrderedStandard: режима «порядок» в Looks
// нет. Карточки стоят в правильном порядке, задание не проверяется.
func TestToLooksSequenceBecomesOrderedStandard(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 2, Rows: 2},
		Blocks: []linka.Block{{
			ID: "seq", Type: linka.BlockTypeSequence,
			Elements: []linka.Element{
				{ID: "third", Kind: linka.ElementKindText, Text: "3"},
				{ID: "extra", Kind: linka.ElementKindText, Text: "x"},
				{ID: "first", Kind: linka.ElementKindText, Text: "1"},
				{ID: "second", Kind: linka.ElementKindText, Text: "2"},
			},
			Sequence: []linka.SeqItem{
				{ElementID: "second", Order: 2},
				{ElementID: "first", Order: 1},
				{ElementID: "third", Order: 3},
			},
		}},
	}
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	page := got.Pages[0]
	if page.Mode != linka.LooksModeStandard {
		t.Fatalf("mode = %q, want standard", page.Mode)
	}
	want := []string{"first", "second", "third", "extra"}
	for i, id := range want {
		if page.Cards[i].ID != id {
			t.Fatalf("card %d = %q, want %q (order %v)", i, page.Cards[i].ID, id, want)
		}
		if page.Cards[i].Answer != nil {
			t.Fatalf("card %s must not be an answer", id)
		}
	}
}

func categoriesConfig(categories []linka.Category, elements []linka.Element) *linka.Config {
	return &linka.Config{
		Settings: linka.Settings{Columns: 3, Rows: 3},
		Blocks: []linka.Block{{
			ID: "cat", Type: linka.BlockTypeCategories,
			Elements: elements, Category: categories,
		}},
	}
}

// TestToLooksCategoriesBecomeMatchGroups: заголовок категории сверху,
// её элементы снизу с тем же matchId. Looks требует соединить
// заголовок с каждым элементом группы.
func TestToLooksCategoriesBecomeMatchGroups(t *testing.T) {
	cfg := categoriesConfig(
		[]linka.Category{
			{ID: "forest", ElementID: "forest-card", Items: []string{"bear", "fox"}},
			{ID: "sea", Name: "Море", Items: []string{"fish"}},
		},
		[]linka.Element{
			{ID: "forest-card", Kind: linka.ElementKindNormal, Text: "Лес",
				Image: &linka.ElementImage{MediaURL: "media/forest.png"}},
			{ID: "bear", Kind: linka.ElementKindText, Text: "Медведь"},
			{ID: "fish", Kind: linka.ElementKindText, Text: "Рыба"},
			{ID: "fox", Kind: linka.ElementKindText, Text: "Лиса"},
			{ID: "gap", Kind: linka.ElementKindEmpty},
		},
	)
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	page := got.Pages[0]
	if page.Mode != linka.LooksModeMatch || page.Rows != 2 {
		t.Fatalf("mode/rows = %q/%d, want match/2", page.Mode, page.Rows)
	}
	if *page.TopColumns != 2 || *page.BottomColumns != 3 {
		t.Fatalf("lanes = %d/%d, want 2/3", *page.TopColumns, *page.BottomColumns)
	}
	type card struct{ id, title, image, match string }
	want := []card{
		{"forest-card", "Лес", "media/forest.png", "forest"},
		{"cat-sea", "Море", "", "sea"},
		{"bear", "Медведь", "", "forest"},
		{"fox", "Лиса", "", "forest"},
		{"fish", "Рыба", "", "sea"},
	}
	if len(page.Cards) != len(want) {
		t.Fatalf("cards = %d, want %d", len(page.Cards), len(want))
	}
	for i, w := range want {
		c := page.Cards[i]
		if c.ID != w.id || c.Title != w.title || c.ImagePath != w.image || c.MatchID != w.match {
			t.Fatalf("card %d = %+v, want %+v", i, c, w)
		}
	}
}

func TestToLooksRejectsUnrepresentableCategories(t *testing.T) {
	text := func(id string) linka.Element { return linka.Element{ID: id, Kind: linka.ElementKindText, Text: id} }
	cases := map[string]*linka.Config{
		"no categories": categoriesConfig(nil, []linka.Element{text("a")}),
		"element outside categories": categoriesConfig(
			[]linka.Category{{ID: "c1", Name: "c1", Items: []string{"a"}}},
			[]linka.Element{text("a"), text("lonely")},
		),
		"element in two categories": categoriesConfig(
			[]linka.Category{
				{ID: "c1", Name: "c1", Items: []string{"a"}},
				{ID: "c2", Name: "c2", Items: []string{"a"}},
			},
			[]linka.Element{text("a")},
		),
		"empty category": categoriesConfig(
			[]linka.Category{{ID: "c1", Name: "c1", Items: []string{"a"}}, {ID: "c2", Name: "c2"}},
			[]linka.Element{text("a")},
		),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := linka.ToLooks(cfg)
			if !errors.Is(err, linka.ErrLooksUnrepresentableCategories) {
				t.Fatalf("err = %v, want ErrLooksUnrepresentableCategories", err)
			}
		})
	}
}

func TestToLooksRejectsUnrepresentableMatching(t *testing.T) {
	base := func(pairs []linka.Pair, elements []linka.Element) *linka.Config {
		return &linka.Config{
			Settings: linka.Settings{Columns: 2, Rows: 2},
			Blocks:   []linka.Block{{ID: "m", Type: linka.BlockTypeMatching, Elements: elements, Pairs: pairs}},
		}
	}
	elements := []linka.Element{
		{ID: "a", Kind: linka.ElementKindText, Text: "a"},
		{ID: "b", Kind: linka.ElementKindText, Text: "b"},
		{ID: "c", Kind: linka.ElementKindText, Text: "c"},
	}

	t.Run("element in two pairs", func(t *testing.T) {
		cfg := base([]linka.Pair{{LeftID: "a", RightID: "b"}, {LeftID: "a", RightID: "c"}}, elements)
		if _, err := linka.ToLooks(cfg); !errors.Is(err, linka.ErrLooksUnrepresentableMatching) {
			t.Fatalf("err = %v, want ErrLooksUnrepresentableMatching", err)
		}
	})

	t.Run("element outside pairs", func(t *testing.T) {
		cfg := base([]linka.Pair{{LeftID: "a", RightID: "b"}}, elements)
		if _, err := linka.ToLooks(cfg); !errors.Is(err, linka.ErrLooksUnrepresentableMatching) {
			t.Fatalf("err = %v, want ErrLooksUnrepresentableMatching", err)
		}
	})

	t.Run("unknown element", func(t *testing.T) {
		cfg := base([]linka.Pair{{LeftID: "a", RightID: "zz"}}, elements[:2])
		if _, err := linka.ToLooks(cfg); !errors.Is(err, linka.ErrLooksUnrepresentableMatching) {
			t.Fatalf("err = %v, want ErrLooksUnrepresentableMatching", err)
		}
	})
}

func TestToLooksRequiresMediaPath(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 2, Rows: 2},
		Blocks: []linka.Block{{
			ID:       "g",
			Type:     linka.BlockTypeGrid,
			Elements: []linka.Element{{ID: "img", Kind: linka.ElementKindNormal, Image: &linka.ElementImage{}}},
		}},
	}
	if _, err := linka.ToLooks(cfg); !errors.Is(err, linka.ErrLooksMissingMediaPath) {
		t.Fatalf("err = %v, want ErrLooksMissingMediaPath", err)
	}
}

// TestToLooksUsesBlockLayout — сетка страницы Looks берётся из блока, а
// не из настроек набора: у каждого задания своя раскладка.
func TestToLooksUsesBlockLayout(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 3, Rows: 3},
		Blocks: []linka.Block{
			{
				ID: "own", Type: linka.BlockTypeGrid,
				Layout: &linka.Layout{Rows: 2, Columns: 2},
				Elements: []linka.Element{
					{ID: "a", Kind: linka.ElementKindText, Text: "1"},
					{ID: "b", Kind: linka.ElementKindText, Text: "2"},
				},
			},
			{
				// Без layout — старый набор, действует сетка из settings.
				ID: "legacy", Type: linka.BlockTypeGrid,
				Elements: []linka.Element{{ID: "c", Kind: linka.ElementKindText, Text: "3"}},
			},
		},
	}
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	own, legacy := got.Pages[0], got.Pages[1]
	if own.Columns != 2 || own.Rows != 2 {
		t.Errorf("own layout: got %dx%d, want 2x2", own.Columns, own.Rows)
	}
	if len(own.Cards) != 4 {
		t.Errorf("own cards = %d, want 4 (2x2 with padding)", len(own.Cards))
	}
	if legacy.Columns != 3 || legacy.Rows != 3 {
		t.Errorf("legacy layout: got %dx%d, want settings 3x3", legacy.Columns, legacy.Rows)
	}
}

// TestToLooksCompositeCard — карточка Looks несёт подпись, картинку и
// озвучку вместе; составной элемент отображается в неё один к одному.
// Раньше это было невозможно: элемент нёс ровно одно вложение.
func TestToLooksCompositeCard(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 2, Rows: 2},
		Blocks: []linka.Block{{
			ID: "b", Type: linka.BlockTypeGrid,
			Elements: []linka.Element{
				{
					ID: "cat", Kind: linka.ElementKindNormal, Text: "Кошка",
					Image: &linka.ElementImage{MediaURL: "media/cat.png"},
					Audio: &linka.ElementAudio{MediaURL: "media/cat.wav", Text: "рыжий кот"},
				},
				{ID: "gap", Kind: linka.ElementKindSpace},
				{ID: "hole", Kind: linka.ElementKindEmpty},
				{ID: "word", Kind: linka.ElementKindText, Text: "да"},
			},
		}},
	}
	got, err := linka.ToLooks(cfg)
	if err != nil {
		t.Fatalf("ToLooks: %v", err)
	}
	cards := got.Pages[0].Cards

	cat := cards[0]
	if cat.CardType != linka.LooksCardTypeContent {
		t.Errorf("cat cardType = %d, want content", cat.CardType)
	}
	if cat.Title != "Кошка" || cat.ImagePath != "media/cat.png" || cat.AudioPath != "media/cat.wav" {
		t.Errorf("composite card lost attributes: %+v", cat)
	}
	if cards[1].CardType != linka.LooksCardTypeSpace {
		t.Errorf("space cardType = %d, want %d", cards[1].CardType, linka.LooksCardTypeSpace)
	}
	if cards[2].CardType != linka.LooksCardTypeEmpty {
		t.Errorf("empty cardType = %d, want %d", cards[2].CardType, linka.LooksCardTypeEmpty)
	}
	if cards[3].CardType != linka.LooksCardTypeContent || cards[3].Title != "да" {
		t.Errorf("text card: %+v", cards[3])
	}
}

// TestToLooksRequiresArchivePathForEachAttachment: путь нужен у каждого
// вложения отдельно — картинка без пути не должна прятаться за озвучкой.
func TestToLooksRequiresArchivePathForEachAttachment(t *testing.T) {
	cfg := &linka.Config{
		Settings: linka.Settings{Columns: 1, Rows: 1},
		Blocks: []linka.Block{{
			ID: "b", Type: linka.BlockTypeGrid,
			Elements: []linka.Element{{
				ID: "cat", Kind: linka.ElementKindNormal,
				Image: &linka.ElementImage{},
				Audio: &linka.ElementAudio{MediaURL: "media/cat.wav"},
			}},
		}},
	}
	if _, err := linka.ToLooks(cfg); !errors.Is(err, linka.ErrLooksMissingMediaPath) {
		t.Fatalf("err = %v, want ErrLooksMissingMediaPath", err)
	}
}
