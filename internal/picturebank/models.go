// Package picturebank provides a protected proxy over interchangeable Pictures Bank sources.
package picturebank

// Category describes one Pictures Bank category. ID is adapter-specific and opaque to clients.
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Picture describes searchable Pictures Bank metadata.
type Picture struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MIMEType   string     `json:"mimeType,omitempty"`
	Categories []Category `json:"categories"`
}

// Image is a bounded image response fetched from Pictures Bank.
type Image struct {
	Data        []byte
	ContentType string
}
