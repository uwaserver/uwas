package apps

// Regression test for F54300: a PUT that patches docker.* fields without
// repeating "runtime":"docker" was answered 200 while the stored definition
// kept the old image/volumes/extra args (the patch was gated on the request's
// runtime instead of the app's).

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

func TestUpdateDockerPatchWithoutRuntime(t *testing.T) {
	setup := func(t *testing.T) (*Handler, *apps.Manager) {
		t.Helper()
		mgr := apps.NewManager(apps.NewStore(t.TempDir()), nil)
		a := &apps.App{Name: "dk", Runtime: apps.RuntimeDocker, Disabled: true,
			Docker: apps.DockerSpec{Image: "old:1", ContainerPort: 80}}
		if err := mgr.Register(a); err != nil {
			t.Fatalf("register: %v", err)
		}
		return New(&updInvalidDeps{mgr: mgr}), mgr
	}

	t.Run("control with runtime", func(t *testing.T) {
		h, mgr := setup(t)
		if code := updInvalidPut(h, "dk", `{"runtime":"docker","disabled":true,"docker":{"image":"new:2"}}`); code != http.StatusOK {
			t.Fatalf("code=%d", code)
		}
		def, _ := mgr.Store().Get("dk")
		if def == nil || def.Docker.Image != "new:2" {
			t.Fatalf("image=%+v", def)
		}
	})
	t.Run("without runtime", func(t *testing.T) {
		h, mgr := setup(t)
		if code := updInvalidPut(h, "dk", `{"disabled":true,"docker":{"image":"new:2","container_port":8080,"volumes":["data:/data"]}}`); code != http.StatusOK {
			t.Fatalf("code=%d", code)
		}
		def, _ := mgr.Store().Get("dk")
		if def == nil || def.Docker.Image != "new:2" || def.Docker.ContainerPort != 8080 || len(def.Docker.Volumes) != 1 {
			t.Fatalf("EXPECTED image new:2 port 8080 volumes [data:/data]; ACTUAL %+v", def)
		}
	})
	t.Run("invalid volume still rejected without runtime", func(t *testing.T) {
		h, _ := setup(t)
		if code := updInvalidPut(h, "dk", `{"disabled":true,"docker":{"volumes":["/:/host"]}}`); code != http.StatusBadRequest {
			t.Fatalf("code=%d want 400", code)
		}
	})
}
