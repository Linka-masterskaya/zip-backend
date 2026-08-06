package tts

type TTSData struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}

type TTSResponse struct {
	JobID string `json:"job_id"`
}
