package analytics

import (
	"encoding/json"
	"net/http"
)

func jsonEncode(w http.ResponseWriter, data any) {
	json.NewEncoder(w).Encode(data)
}
