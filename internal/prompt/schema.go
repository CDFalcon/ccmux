package prompt

import "time"

const CurrentSchemaVersion = 1

type Prompt struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type storeData struct {
	Version int                `json:"version"`
	Prompts map[string]*Prompt `json:"prompts"`
}
