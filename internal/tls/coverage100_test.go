package uwastls

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// --- SetOnCertRenewed / SetOnCertExpiry ---

func TestSetOnCertRenewed(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	called := false
	m.SetOnCertRenewed(func(host string) {
		called = true
	})
	if m.onCertRenewed == nil {
		t.Error("onCertRenewed callback should be set")
	}
	m.onCertRenewed("test.com")
	if !called {
		t.Error("onCertRenewed callback should have been invoked")
	}
}

func TestSetOnCertExpiry(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	var gotHost string
	var gotDays int
	m.SetOnCertExpiry(func(host string, daysLeft int) {
		gotHost = host
		gotDays = daysLeft
	})
	if m.onCertExpiry == nil {
		t.Error("onCertExpiry callback should be set")
	}
	m.onCertExpiry("expiring.com", 5)
	if gotHost != "expiring.com" || gotDays != 5 {
		t.Errorf("onCertExpiry called with (%q, %d), want (expiring.com, 5)", gotHost, gotDays)
	}
}

// --- RenewCert ---

func TestRenewCertNoACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	err := m.RenewCert(context.Background(), "test.com")
	if err == nil {
		t.Error("RenewCert should fail without ACME client")
	}
}

func TestRenewCertWithACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	// RenewCert should attempt to obtainCert but fail because the ACME server isn't real
	err := m.RenewCert(context.Background(), "EXAMPLE.COM")
	if err == nil {
		t.Error("RenewCert should fail when ACME server is unreachable")
	}
}

// --- CertStatus ---

func TestCertStatusFound(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "status.com")
	m.certs.Store("status.com", cert)

	info := m.CertStatus("status.com")
	if info == nil {
		t.Fatal("CertStatus should return info for loaded cert")
	}
	if info.Issuer != "status.com" {
		t.Errorf("Issuer = %q, want status.com", info.Issuer)
	}
	if info.Expiry.IsZero() {
		t.Error("Expiry should not be zero")
	}
	if info.DaysLeft < 0 {
		t.Errorf("DaysLeft = %d, should be >= 0", info.DaysLeft)
	}
}

func TestCertStatusNotFound(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	info := m.CertStatus("nonexistent.com")
	if info != nil {
		t.Error("CertStatus should return nil for unknown host")
	}
}

func TestCertStatusEmptyCert(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Store a cert with no Certificate data (leafCert will fail)
	emptyCert := &tls.Certificate{}
	m.certs.Store("empty.com", emptyCert)

	info := m.CertStatus("empty.com")
	if info != nil {
		t.Error("CertStatus should return nil when leafCert fails")
	}
}

func TestCertStatusCaseInsensitive(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "upper.com")
	m.certs.Store("upper.com", cert)

	info := m.CertStatus("UPPER.COM")
	if info == nil {
		t.Error("CertStatus should handle case insensitivity")
	}
}

// --- checkRenewals: triggers onCertExpiry callback on failure ---

func TestCheckRenewalsCallsOnCertExpiry(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	var callbackHost string
	var callbackDays int
	m.SetOnCertExpiry(func(host string, daysLeft int) {
		callbackHost = host
		callbackDays = daysLeft
	})

	// Create a cert that expires in 10 days (within 30-day renewal window)
	nearExpiry := generateCertWithExpiry(t, "callback-test.com", 10*24*time.Hour)
	m.certs.Store("callback-test.com", nearExpiry)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.checkRenewals(ctx)

	// Renewal should fail (no real ACME), and onCertExpiry should be called
	if callbackHost != "callback-test.com" {
		t.Errorf("onCertExpiry host = %q, want callback-test.com", callbackHost)
	}
	if callbackDays < 0 || callbackDays > 30 {
		t.Errorf("onCertExpiry daysLeft = %d, should be between 0 and 30", callbackDays)
	}
}

// --- checkRenewals: cert not within renewal window ---

func TestCheckRenewalsSkipsHealthyCert(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	expiryCalled := false
	m.SetOnCertExpiry(func(host string, daysLeft int) {
		expiryCalled = true
	})

	healthy := generateCertWithExpiry(t, "healthy-skip.com", 60*24*time.Hour)
	m.certs.Store("healthy-skip.com", healthy)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.checkRenewals(ctx)

	if expiryCalled {
		t.Error("onCertExpiry should not be called for a healthy cert")
	}
}

// --- Storage Save error paths ---

func TestSaveErrorMkdir(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile")
	os.WriteFile(filePath, []byte("x"), 0644)

	storage := NewCertStorage(filePath) // base is a file, not dir
	cert, certPEM, keyPEM := generateTestCert(t, "mkdirerr.com")
	err := storage.Save("mkdirerr.com", cert, keyPEM, certPEM)
	if err == nil {
		t.Error("Save should fail when MkdirAll fails")
	}
}

func TestSaveErrorWriteCert(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	domainDir := filepath.Join(dir, "writeerr.com")
	os.MkdirAll(filepath.Join(domainDir, "cert.pem"), 0700)

	cert, certPEM, keyPEM := generateTestCert(t, "writeerr.com")
	err := storage.Save("writeerr.com", cert, keyPEM, certPEM)
	if err == nil {
		t.Error("Save should fail when writing cert.pem fails")
	}
}

func TestSaveErrorWriteKey(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	domainDir := filepath.Join(dir, "keyerr.com")
	os.MkdirAll(domainDir, 0700)
	os.WriteFile(filepath.Join(domainDir, "cert.pem"), []byte("ok"), 0644)
	os.MkdirAll(filepath.Join(domainDir, "key.pem"), 0700)

	cert, certPEM, keyPEM := generateTestCert(t, "keyerr.com")
	err := storage.Save("keyerr.com", cert, keyPEM, certPEM)
	if err == nil {
		t.Error("Save should fail when writing key.pem fails")
	}
}

func TestSaveErrorWriteMeta(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	domainDir := filepath.Join(dir, "metaerr.com")
	os.MkdirAll(domainDir, 0700)
	os.MkdirAll(filepath.Join(domainDir, "meta.json"), 0700)

	cert, certPEM, keyPEM := generateTestCert(t, "metaerr.com")
	err := storage.Save("metaerr.com", cert, keyPEM, certPEM)
	if err == nil {
		t.Error("Save should fail when writing meta.json fails")
	}
}

// --- ObtainCerts: retry logic with context cancellation ---

func TestObtainCertsContextCancellation(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "cancelme.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ObtainCerts(ctx)

	hello := &tls.ClientHelloInfo{ServerName: "cancelme.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have a cert when context is cancelled")
	}
}

// --- StartRenewal: verify goroutine starts and stops with context ---

func TestStartRenewalWithACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	ctx, cancel := context.WithCancel(context.Background())
	m.StartRenewal(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// --- CertStatus with cert that has Leaf set to nil (DER parsing) ---

func TestCertStatusNoLeaf(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "noleaf-status.com")
	cert.Leaf = nil
	m.certs.Store("noleaf-status.com", cert)

	info := m.CertStatus("noleaf-status.com")
	if info == nil {
		t.Fatal("CertStatus should parse from DER when Leaf is nil")
	}
	if info.Issuer != "noleaf-status.com" {
		t.Errorf("Issuer = %q, want noleaf-status.com", info.Issuer)
	}
}

// --- Save with cert that has Leaf set (covers cert.Leaf != nil branch) ---

func TestSaveWithLeafSet(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "withleaf.com")
	err := storage.Save("withleaf.com", cert, keyPEM, certPEM)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	meta, err := storage.LoadMeta("withleaf.com")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Issuer != "withleaf.com" {
		t.Errorf("Issuer = %q, want withleaf.com", meta.Issuer)
	}
}

// --- ObtainCerts: retry logic that exercises the backoff path ---

func TestObtainCertsRetryBackoff(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "http://127.0.0.1:1/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "retry1.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.ObtainCerts(ctx)

	hello := &tls.ClientHelloInfo{ServerName: "retry1.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have a cert when ACME server is unreachable")
	}
}

// --- checkRenewals: cert with invalid DER data ---

func TestCheckRenewalsSkipsCertWithInvalidDER(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	cert := &tls.Certificate{
		Certificate: [][]byte{{0, 1, 2, 3}}, // garbage DER
	}
	m.certs.Store("badder.com", cert)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.checkRenewals(ctx)
}

// --- LoadExistingCerts: info log path ---

func TestLoadExistingCertsLogsCount(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "logtest.com")
	storage.Save("logtest.com", cert, keyPEM, certPEM)

	log := logger.New("debug", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)
	m.LoadExistingCerts()

	hello := &tls.ClientHelloInfo{ServerName: "logtest.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got == nil {
		t.Error("cert should be loaded")
	}
}

// --- ObtainCerts with context cancelled during retry backoff ---

func TestObtainCertsCancelDuringBackoff(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "http://127.0.0.1:1/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "backoff-cancel.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	m.ObtainCerts(ctx)
}

// --- StartRenewal: full goroutine path ---

func TestStartRenewalFullLoop(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	m := NewManager(cfg, nil, log)
	m.renewalInitialDelay = 10 * time.Millisecond
	m.renewalInterval = 50 * time.Millisecond

	newCert, newCertPEM, newKeyPEM := generateTestCert(t, "renewal-full.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return newCert, newCertPEM, newKeyPEM, nil
	}

	// Store a near-expiry cert so checkRenewals has work to do
	oldCert := generateCertWithExpiry(t, "renewal-full.com", 5*24*time.Hour)
	m.certs.Store("renewal-full.com", oldCert)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	m.StartRenewal(ctx)

	// Wait for the goroutine to execute at least one cycle
	time.Sleep(150 * time.Millisecond)

	// Verify the cert was renewed
	hello := &tls.ClientHelloInfo{ServerName: "renewal-full.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != newCert {
		t.Error("cert should have been renewed via StartRenewal goroutine")
	}
}

// --- StartRenewal: default interval path (renewalInterval == 0) ---

func TestStartRenewalDefaultInterval(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)
	m.renewalInitialDelay = 10 * time.Millisecond
	// Do NOT set renewalInterval — exercises the interval == 0 default path

	ctx, cancel := context.WithCancel(context.Background())
	m.StartRenewal(ctx)

	// Wait for the goroutine to pass initial delay and execute checkRenewals once
	time.Sleep(50 * time.Millisecond)
	// Cancel while waiting on the 12-hour ticker
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// --- StartRenewal: context cancelled during ticker wait ---

func TestStartRenewalCancelDuringTicker(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)
	m.renewalInitialDelay = 10 * time.Millisecond
	m.renewalInterval = 1 * time.Hour // long interval so it blocks on ticker

	ctx, cancel := context.WithCancel(context.Background())
	m.StartRenewal(ctx)

	// Let it pass the initial delay and enter the ticker wait
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Give goroutine time to exit
	time.Sleep(50 * time.Millisecond)
}

// --- LoadAll: non-IsNotExist error using readDirFunc hook ---

func TestLoadAllReadDirError(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)
	storage.readDirFunc = func(name string) ([]os.DirEntry, error) {
		return nil, fmt.Errorf("permission denied")
	}

	_, err := storage.LoadAll()
	if err == nil {
		t.Error("LoadAll should return error when readDir fails with non-IsNotExist error")
	}
}

// --- LoadExistingCerts: storage LoadAll returns error ---

func TestLoadExistingCertsLoadAllError(t *testing.T) {
	dir := t.TempDir()
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)

	// Override readDirFunc to simulate error
	m.storage.readDirFunc = func(name string) ([]os.DirEntry, error) {
		return nil, fmt.Errorf("I/O error")
	}

	// Should not panic; just logs a warning
	m.LoadExistingCerts()

	// No certs should be loaded
	hello := &tls.ClientHelloInfo{ServerName: "anything.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should have no certs when storage LoadAll fails")
	}
}

// --- EncodeCertPEM with empty chain ---

func TestEncodeCertPEMEmpty(t *testing.T) {
	result := EncodeCertPEM(nil)
	if len(result) != 0 {
		t.Errorf("EncodeCertPEM(nil) should return empty, got %d bytes", len(result))
	}
}

// --- obtainCert: successful path using acmeObtainFunc hook ---

func TestObtainCertSuccessful(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)

	testCert, testCertPEM, testKeyPEM := generateTestCert(t, "obtain-ok.com")
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		return testCert, testCertPEM, testKeyPEM, nil
	}

	got, err := m.obtainCert(context.Background(), "obtain-ok.com")
	if err != nil {
		t.Fatalf("obtainCert: %v", err)
	}
	if got != testCert {
		t.Error("should return the test cert")
	}

	// Verify cert is stored in memory
	hello := &tls.ClientHelloInfo{ServerName: "obtain-ok.com"}
	gotFromMap, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate after obtainCert: %v", err)
	}
	if gotFromMap != testCert {
		t.Error("cert should be stored in memory")
	}

	// Verify cert is persisted to disk
	if !m.storage.Exists("obtain-ok.com") {
		t.Error("cert should be persisted to disk")
	}
}

// --- obtainCert: successful but storage persist fails ---

func TestObtainCertStoragePersistFails(t *testing.T) {
	// Use a storage path that will fail writes
	dir := t.TempDir()
	// Create a file where the domain dir should be
	brokenBase := filepath.Join(dir, "broken")
	os.WriteFile(brokenBase, []byte("x"), 0644)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: brokenBase}
	m := NewManager(cfg, nil, log)

	testCert, testCertPEM, testKeyPEM := generateTestCert(t, "persist-fail.com")
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		return testCert, testCertPEM, testKeyPEM, nil
	}

	// Should succeed (storage error is logged but not returned)
	got, err := m.obtainCert(context.Background(), "persist-fail.com")
	if err != nil {
		t.Fatalf("obtainCert: %v", err)
	}
	if got != testCert {
		t.Error("should return the cert even when storage fails")
	}
}

// --- obtainCert with nil acme AND nil acmeObtainFunc ---

func TestObtainCertBothNil(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	_, err := m.obtainCert(context.Background(), "test.com")
	if err == nil {
		t.Error("obtainCert should fail with both nil")
	}
}

// --- checkRenewals: successful renewal path ---

func TestCheckRenewalsSuccessfulRenewal(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	m := NewManager(cfg, nil, log)

	// Prepare a new cert that the mock ACME will return
	newCert, newCertPEM, newKeyPEM := generateTestCert(t, "renew-ok.com")
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		return newCert, newCertPEM, newKeyPEM, nil
	}

	// Track onCertRenewed callback
	var renewedHost string
	m.SetOnCertRenewed(func(host string) {
		renewedHost = host
	})

	// Store a near-expiry cert
	oldCert := generateCertWithExpiry(t, "renew-ok.com", 5*24*time.Hour)
	m.certs.Store("renew-ok.com", oldCert)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.checkRenewals(ctx)

	// Verify the cert was replaced
	hello := &tls.ClientHelloInfo{ServerName: "renew-ok.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != newCert {
		t.Error("cert should have been replaced with the renewed cert")
	}

	// Verify onCertRenewed was called
	if renewedHost != "renew-ok.com" {
		t.Errorf("onCertRenewed host = %q, want renew-ok.com", renewedHost)
	}

	// Verify cert was persisted
	if !m.storage.Exists("renew-ok.com") {
		t.Error("renewed cert should be persisted to disk")
	}
}

// --- checkRenewals: successful renewal but storage.Save fails ---

func TestCheckRenewalsRenewalPersistFails(t *testing.T) {
	// Use broken storage to test the persist error path
	dir := t.TempDir()
	brokenBase := filepath.Join(dir, "broken")
	os.WriteFile(brokenBase, []byte("x"), 0644)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: brokenBase,
	}
	m := NewManager(cfg, nil, log)

	newCert, newCertPEM, newKeyPEM := generateTestCert(t, "persist-err.com")
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		return newCert, newCertPEM, newKeyPEM, nil
	}

	oldCert := generateCertWithExpiry(t, "persist-err.com", 5*24*time.Hour)
	m.certs.Store("persist-err.com", oldCert)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Should not panic; storage error is logged
	m.checkRenewals(ctx)

	// Cert should still be updated in memory even if persist fails
	hello := &tls.ClientHelloInfo{ServerName: "persist-err.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != newCert {
		t.Error("cert should be updated in memory even when persist fails")
	}
}

// --- ObtainCerts: successful obtainment path ---

func TestObtainCertsSuccessful(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	domains := []config.Domain{
		{Host: "obtain-success.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	testCert, testCertPEM, testKeyPEM := generateTestCert(t, "obtain-success.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return testCert, testCertPEM, testKeyPEM, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.ObtainCerts(ctx)

	hello := &tls.ClientHelloInfo{ServerName: "obtain-success.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != testCert {
		t.Error("cert should be obtained and stored")
	}
}

// --- ObtainCerts: retry with some failures then success ---

func TestObtainCertsRetryWithEventualSuccess(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	domains := []config.Domain{
		{Host: "retry-ok.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	callCount := 0
	testCert, testCertPEM, testKeyPEM := generateTestCert(t, "retry-ok.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		callCount++
		if callCount <= 1 {
			return nil, nil, nil, fmt.Errorf("simulated ACME error")
		}
		return testCert, testCertPEM, testKeyPEM, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second) // backoff is 30s for first retry
	defer cancel()
	m.ObtainCerts(ctx)

	hello := &tls.ClientHelloInfo{ServerName: "retry-ok.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != testCert {
		t.Error("cert should be obtained after retry")
	}
}

// --- ObtainCerts: all retries fail, pending hosts logged ---

func TestObtainCertsAllRetriesFail(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "allfail.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return nil, nil, nil, fmt.Errorf("always fails")
	}

	// Needs enough time for 3 attempts with backoff: 0 + 30s + 60s
	// Use context with shorter timeout so it bails faster
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	m.ObtainCerts(ctx)

	// Should not have any cert
	hello := &tls.ClientHelloInfo{ServerName: "allfail.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have a cert when all retries fail")
	}
}

// --- GetCertificate: on-demand successful obtainment ---

func TestGetCertificateOnDemandSuccess(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:    "test@example.com",
		CAURL:    "https://acme.example.com/directory",
		Storage:  t.TempDir(),
		OnDemand: true,
	}
	m := NewManager(cfg, nil, log)

	testCert, testCertPEM, testKeyPEM := generateTestCert(t, "ondemand-ok.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return testCert, testCertPEM, testKeyPEM, nil
	}

	hello := &tls.ClientHelloInfo{ServerName: "ondemand-ok.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate on-demand: %v", err)
	}
	if got != testCert {
		t.Error("should return the on-demand obtained cert")
	}
}

// --- StartRenewal: actually runs checkRenewals loop ---

func TestStartRenewalRunsCheckRenewals(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	m := NewManager(cfg, nil, log)

	// Use a near-expiry cert so checkRenewals triggers
	newCert, newCertPEM, newKeyPEM := generateTestCert(t, "renewal-loop.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return newCert, newCertPEM, newKeyPEM, nil
	}

	oldCert := generateCertWithExpiry(t, "renewal-loop.com", 5*24*time.Hour)
	m.certs.Store("renewal-loop.com", oldCert)

	// We can't wait 1 minute for the initial delay in production code.
	// Instead, test checkRenewals directly (which StartRenewal calls).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.checkRenewals(ctx)

	hello := &tls.ClientHelloInfo{ServerName: "renewal-loop.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != newCert {
		t.Error("cert should have been renewed")
	}
}

// --- checkRenewals: without onCertRenewed callback (nil) ---

func TestCheckRenewalsRenewalWithoutCallback(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: dir,
	}
	m := NewManager(cfg, nil, log)

	newCert, newCertPEM, newKeyPEM := generateTestCert(t, "no-callback.com")
	m.acmeObtainFunc = func(ctx context.Context, doms []string) (*tls.Certificate, []byte, []byte, error) {
		return newCert, newCertPEM, newKeyPEM, nil
	}

	// Don't set onCertRenewed - leave it nil
	oldCert := generateCertWithExpiry(t, "no-callback.com", 5*24*time.Hour)
	m.certs.Store("no-callback.com", oldCert)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.checkRenewals(ctx)

	// Should succeed without panic even without callback
	hello := &tls.ClientHelloInfo{ServerName: "no-callback.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != newCert {
		t.Error("cert should be renewed")
	}
}
