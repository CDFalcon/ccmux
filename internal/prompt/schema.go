package prompt

import "time"

const CurrentSchemaVersion = 1

type Prompt struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Content      string    `json:"content"`
	IsDefault    bool      `json:"is_default"`
	ProjectNames []string  `json:"project_names,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (p *Prompt) AppliesToProject(projectName string) bool {
	if len(p.ProjectNames) == 0 {
		return true
	}
	for _, name := range p.ProjectNames {
		if name == projectName {
			return true
		}
	}
	return false
}

type storeData struct {
	Version int                `json:"version"`
	Prompts map[string]*Prompt `json:"prompts"`
}
