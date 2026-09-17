package server

import (
	"encoding/json"
	"net/http"

	"agr/config"
)

type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// modelsHandler lists configured models using IDs accepted by the router.
func modelsHandler(cfg *config.Config) http.HandlerFunc {
	models := make([]modelEntry, 0)
	seen := make(map[string]bool)
	for _, provider := range cfg.Providers {
		for _, model := range provider.Models {
			id := provider.Name + "/" + model
			if model == "" || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, modelEntry{
				ID: id, Object: "model", OwnedBy: provider.Name,
			})
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodHead {
			return
		}
		json.NewEncoder(w).Encode(struct {
			Object string       `json:"object"`
			Data   []modelEntry `json:"data"`
		}{Object: "list", Data: models})
	}
}
