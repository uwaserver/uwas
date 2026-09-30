package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileListPaginatesEveryEntry(t *testing.T) {
	s, root := testServerWithRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "adir"), 0755); err != nil {
		t.Fatal(err)
	}
	const fileCount = 60
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	first := listFiles(t, s, "/api/v1/files/example.com/list?path=.")
	if first.Total != fileCount+1 {
		t.Fatalf("total = %d, want %d", first.Total, fileCount+1)
	}
	if first.Limit != 50 || first.Offset != 0 {
		t.Fatalf("page window = limit %d offset %d, want 50/0", first.Limit, first.Offset)
	}
	if len(first.Items) != 50 {
		t.Fatalf("first page len = %d, want 50", len(first.Items))
	}
	if first.Items[0].Name != "adir" || !first.Items[0].IsDir {
		t.Fatalf("first entry = %+v, want directory adir", first.Items[0])
	}

	second := listFiles(t, s, "/api/v1/files/example.com/list?path=.&offset=50")
	if second.Total != first.Total {
		t.Fatalf("second total = %d, want %d", second.Total, first.Total)
	}
	if len(second.Items) != 11 {
		t.Fatalf("second page len = %d, want 11", len(second.Items))
	}

	seen := map[string]bool{}
	for _, page := range []fileListBody{first, second} {
		for _, item := range page.Items {
			if seen[item.Name] {
				t.Fatalf("duplicate entry %s", item.Name)
			}
			seen[item.Name] = true
		}
	}
	if len(seen) != fileCount+1 {
		t.Fatalf("covered %d names, want %d", len(seen), fileCount+1)
	}
	if !seen["file-59.txt"] || !seen["adir"] {
		t.Fatalf("missing edge names: file-59=%v adir=%v", seen["file-59.txt"], seen["adir"])
	}

	filtered := listFiles(t, s, "/api/v1/files/example.com/list?path=.&q=file-5")
	if filtered.Total != 10 {
		t.Fatalf("filtered total = %d, want 10 (file-50..file-59)", filtered.Total)
	}
	for _, item := range filtered.Items {
		if item.Name == "adir" || item.Name == "file-05.txt" {
			t.Fatalf("filter matched %s", item.Name)
		}
	}
}

type fileListBody struct {
	Items []struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
	} `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func listFiles(t *testing.T, s *Server, rawURL string) fileListBody {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, rawURL, nil)
	req.SetPathValue("domain", "example.com")
	s.handleFileList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d, body: %s", rawURL, rec.Code, rec.Body.String())
	}
	var body fileListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
