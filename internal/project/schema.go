package project

const CurrentSchemaVersion = 1

type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type storeData struct {
	Version  int                 `json:"version"`
	Projects map[string]*Project `json:"projects"`
}
