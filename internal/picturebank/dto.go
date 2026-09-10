package picturebank

type PictureResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIMEType string `json:"mimeType,omitempty"`
	// Categories отдаёт и идентификатор, и имя: по одному имени клиент
	// не может открыть листинг категории, а именно он и нужен после
	// показа картинки. Форма совпадает с GET /pictures/categories.
	Categories []Category `json:"categories"`
	URL        string     `json:"url"`
}

func toPictureResponse(pic Picture) PictureResponse {
	// Пустой список вместо null: поле обязательное по контракту.
	categories := pic.Categories
	if categories == nil {
		categories = []Category{}
	}
	return PictureResponse{
		ID:         pic.ID,
		Name:       pic.Name,
		MIMEType:   pic.MIMEType,
		Categories: categories,
		URL:        "/api/v1/pictures/" + pic.ID + "/content",
	}
}

func toPictureResponses(pics []Picture) []PictureResponse {
	result := make([]PictureResponse, 0, len(pics))
	for _, pic := range pics {
		result = append(result, toPictureResponse(pic))
	}
	return result
}
