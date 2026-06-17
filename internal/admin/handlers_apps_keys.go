package admin

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

type AppDeployKeyResponse struct {
	PrivateKeyPath string `json:"private_key_path"`
	PublicKey      string `json:"public_key"`
}

func (s *Server) handleAppGenerateDeployKey(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	name := r.PathValue("name")
	def, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	privatePath, publicKey, err := generateAppDeployKey(s.appsMgr.Store().Dir, name)
	if err != nil {
		s.recordAuditR(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	def.Deploy.SSHKeyPath = privatePath
	if err := validateDeployConfig(def); err != nil {
		s.recordAuditR(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.appsMgr.Store().Save(def); err != nil {
		s.recordAuditR(r, "app.deploy_key", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.recordAuditR(r, "app.deploy_key", name, true)
	jsonResponse(w, AppDeployKeyResponse{
		PrivateKeyPath: privatePath,
		PublicKey:      publicKey,
	})
}

func generateAppDeployKey(storeDir, appName string) (string, string, error) {
	if storeDir == "" {
		return "", "", fmt.Errorf("apps store directory is not configured")
	}
	keyDir := filepath.Join(storeDir, "deploy-keys", appName)
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", "", fmt.Errorf("create deploy key dir: %w", err)
	}
	if err := os.Chmod(keyDir, 0700); err != nil {
		return "", "", fmt.Errorf("chmod deploy key dir: %w", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 deploy key: %w", err)
	}
	privateBlock, err := ssh.MarshalPrivateKey(priv, "uwas "+appName+" deploy key")
	if err != nil {
		return "", "", fmt.Errorf("marshal deploy private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("marshal deploy public key: %w", err)
	}

	privatePath := filepath.Join(keyDir, "id_ed25519")
	tmp := privatePath + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(privateBlock), 0600); err != nil {
		return "", "", fmt.Errorf("write deploy private key: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("chmod deploy private key: %w", err)
	}
	if err := os.Rename(tmp, privatePath); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("install deploy private key: %w", err)
	}
	return privatePath, string(ssh.MarshalAuthorizedKey(sshPub)), nil
}
