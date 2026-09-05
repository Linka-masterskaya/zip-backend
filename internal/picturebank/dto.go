package picturebank

type PictureResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	MIMEType   string   `json:"mimeType,omitempty"`
	Categories []string `json:"categories"`
	URL        string   `json:"url"`
}

func toPictureResponse(pic Picture) PictureResponse {
	categories := make([]string, 0, len(pic.Categories))
	for _, cat := range pic.Categories {
		categories = append(categories, cat.Name)
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
