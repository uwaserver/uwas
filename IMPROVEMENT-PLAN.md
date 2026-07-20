# UWAS — İyileştirme Planı

**Temel:** COMPREHENSIVE-REVIEW.md (2026-07-15)  
**Mevcut Kanban panoları:** "Deep Scan Results", "Coverage Improvement Goals"  
**Sürüm:** v0.8.9 | **Risk Skoru:** 2.1/10 | **Kapsama:** 90.9%

---

## İçindekiler

1. [Yöntem](#1-yöntem)
2. [Faz Özeti](#2-faz-özeti)
3. [Faz 1: Hızlı Kazanımlar (1-2 gün)](#3-faz-1-hızlı-kazanımlar-1-2-gün)
4. [Faz 2: Test Altyapısı & Kalite (1 hafta)](#4-faz-2-test-altyapısı--kalite-1-hafta)
5. [Faz 3: Ön Yüz & Erişilebilirlik (1-2 hafta)](#5-faz-3-ön-yüz--erişilebilirlik-1-2-hafta)
6. [Faz 4: Mimari & Kod Kalitesi (2-3 hafta)](#6-faz-4-mimari--kod-kalitesi-2-3-hafta)
7. [Faz 5: Dokümantasyon & Operasyon (1 hafta)](#7-faz-5-dokümantasyon--operasyon-1-hafta)
8. [Bağımlılık Grafiği](#8-bağımlılık-grafiği)
9. [Mevcut Kanban ile İlişki](#9-mevcut-kanban-ile-i̇lişki)
10. [Risk Değerlendirmesi](#10-risk-değerlendirmesi)
11. [Metrikler & Başarı Kriterleri](#11-metrikler--başarı-kriterleri)
12. [Önerilen Sprint Planlaması](#12-önerilen-sprint-planlaması)

---

## 1. Yöntem

Bu plan, COMPREHENSIVE-REVIEW.md raporundaki **25 iyileştirme maddesi** temel alınarak hazırlanmıştır. Her madde:

- **Faz** — hangi aşamada ele alınacağı
- **Öncelik** — High / Medium / Low
- **Çaba** — Small (< 2h) / Medium (2-4h) / Large (4-8h) / XL (> 8h)
- **Bağımlılık** — hangi maddelerin önce tamamlanması gerektiği
- **Kanban referansı** — varsa mevcut board'daki task ID'si

Plan, **6 faza** ayrılmıştır. Her faz bağımsız olarak ele alınabilir, ancak bağımlılıklar belirtilmiştir.

---

## 2. Faz Özeti

| Faz | Adı | Süre | Madde | Öncelik |
|:---:|:---|:---:|:---:|:---:|
| 1 | **Hızlı Kazanımlar** | 1-2 gün | 6 | Yüksek |
| 2 | **Test Altyapısı & Kalite** | 1 hafta | 8 | Yüksek |
| 3 | **Ön Yüz & Erişilebilirlik** | 1-2 hafta | 5 | Orta |
| 4 | **Mimari & Kod Kalitesi** | 2-3 hafta | 7 | Orta |
| 5 | **Dokümantasyon & Operasyon** | 1 hafta | 6 | Düşük |
| 6 | **Sürekli İyileştirme** | Sürekli | 3 | Düşük |
| | **Toplam** | **~6-9 hafta** | **35** | |

---

## 3. Faz 1: Hızlı Kazanımlar (1-2 gün)

Düşük çaba, yüksek etki — hemen yapılabilir.

### 1.1 Statik Kontrol Uyarılarını Temizleme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Code Quality |
| **Öncelik** | High |
| **Çaba** | Small (~1 saat) |
| **Bağımlılık** | Yok |
| **Kanban** | Deep Scan → "Code Quality & Lint" sütununda kayıtlı |

**4 staticcheck bulgusu:**

| # | Dosya | Satır | Sorun | Çözüm |
|:---|:------|:-----|:------|:------|
| 1 | `admin_coverage12_test.go` | 57 | `clearAuditBuf` kullanılmıyor (U1000) | Fonksiyonu sil |
| 2 | `admin_coverage14_test.go` | 58 | `rand.Read` deprecated (SA1019) | `crypto/rand.Read` ile değiştir |
| 3 | `auth/edge_test.go` | 258 | Boş kritik bölüm (SA2001) | Lock/Unlock arasını anlamlı kodla doldur veya kaldır |
| 4 | `fastcgi_coverage_test.go` | 341 | `make(chan *conn, 0)` → `make(chan *conn)` (S1019) | Sıfır buffer bildirimini kısalt |

### 1.2 gofmt Formatlama Sorunlarını Düzeltme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | Deep Scan Kanban → Code Quality |
| **Öncelik** | Medium |
| **Çaba** | Small (~30 dk) |
| **Bağımlılık** | Yok |

**10+ test dosyasında** gofmt uyarısı var. `gofmt -w` ile düzeltilebilir. Muhtemelen otomatik test oluşturma araçları tarafından üretilmiş dosyalar.

### 1.3 Sihirli Sayıları Merkezileştirme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Code Quality |
| **Öncelik** | Medium |
| **Çaba** | Small (~1 saat) |
| **Bağımlılık** | Yok |

Şu an dosyalara dağılmış sabitler:

```go
maxConcurrentWrites = 16       // cache/engine.go
maxRecentBlocked    = 200      // middleware/botguard.go
maxLogEntries       = 1000     // admin/api.go
listeningProbeTimeout = 3s     // admin/handlers_apps.go
onDemandMaxPerMinute = 10      // tls/manager.go
maxRetryBodyBytes   = 8 << 20  // handler/proxy/handler.go
```

**Öneri:** Her paket için bir `constants.go` dosyası oluşturmak yerine, tüm proje sabitlerini `internal/config/constants.go` gibi tek bir merkezde toplamak. Alternatif: İlgili paketlerde `const` blokları halinde düzenlemek.

### 1.4 CSP `unsafe-inline` İyileştirmesi

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Security |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

Dashboard CSP'de `style-src 'self' 'unsafe-inline'` bulunuyor. Vite'nin ürettiği CSS hash'leri veya nonce tabanlı yaklaşım kullanılabilir.

**Seçenekler:**
1. Vite'nin `css.hashAlgorithm` yapılandırmasını kullanarak hash-based CSP
2. Express/vite middleware ile nonce enjekte etme
3. **Şimdilik kabul** — Tailwind'in derleme zamanında ürettiği CSS sabit olduğu için risk düşük

### 1.5 TOTP Adım Penceresi Dokümantasyonu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Security |
| **Öncelik** | Low |
| **Çaba** | Small (~15 dk) |
| **Bağımlılık** | Yok |

TOTP'nin ±1 adım toleransını kod yorumunda ve varsa kullanıcı belgelerinde açıkça belirtmek. Mevcut kodda `totp.go` içinde `validateTOTPNoReplay` fonksiyonu var, tolerans sabitlenmiş.

### 1.6 Bcrypt Maliyet Yükseltmesi

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Security |
| **Öncelik** | Low |
| **Çaba** | Small (~15 dk) |
| **Bağımlılık** | Yok |

Mevcut bcrypt cost değeri 12. Modern donanımda (Ryzen 9 9950X3D gibi) cost 14 daha uygun. Yalnızca **yeni** şifreler için geçerli olacak şekilde değiştirilmeli — mevcut hash'ler bozulmamalı.

---

## 4. Faz 2: Test Altyapısı & Kalite (1 hafta)

### 2.1 Kritik Sıfır Kapsama Fonksiyonları

| # | Fonksiyon | Dosya | Mevcut | Hedef | Kanban |
|:---|:----------|:------|:------:|:-----:|:-------|
| 1 | `terminalHandler` | handlers_terminal.go:14 | 0% | >80% | Her iki board'da kayıtlı |
| 2 | `daemonize` | cli/daemon_unix.go:14 | 0% | >80% | Her iki board'da kayıtlı |
| 3 | `phpVersionInstalled` | handlers_setup.go:43 | 0% | >80% | Deep Scan board'unda |
| 4 | `Unwrap` | middleware/compress.go:235 | 0% | >80% | Deep Scan board'unda |
| 5 | `MightMatch` | rewrite/engine.go:47 | 0% | >80% | Deep Scan board'unda |
| 6 | `handleUserCreate` | handlers_auth.go:267 | 38.5% | >80% | Deep Scan board'unda |
| 7 | `handleAppStart/Restart/Stop` | handlers_apps.go | 37-60% | >80% | Her iki board'da ✅ işaretli |

| Özellik | Detay |
|:--------|:------|
| **Öncelik** | **High** (terminalHandler, daemonize) |
| **Çaba** | Large (~6-8 saat toplam) |
| **Bağımlılık** | Yok |

### 2.2 Yüksek Etkili Kapsama Boşlukları

| Fonksiyon Grubu | Mevcut | Hedef | Çaba |
|:----------------|:------:|:-----:|:----:|
| Cloudflare tunnel handlers | 20-48% | >80% | Large (4s) |
| DB handler functions | 43-53% | >80% | Medium (3s) |
| WordPress handlers | 40-58% | >80% | Medium (3s) |
| PHP handlers | 22-50% | >80% | Medium (3s) |
| Git deploy pipeline | 49-64% | >80% | Medium (2s) |
| Admin auth middleware | 70.5% | >85% | Small (1s) |

| Özellik | Detay |
|:--------|:------|
| **Öncelik** | High |
| **Çaba** | Large (~16 saat toplam) |
| **Bağımlılık** | 2.1 tamamlanmalı (altyapı) |
| **Kanban** | Her iki board'da detaylı kayıtlı |

### 2.3 Ön Yüz Test Altyapısı

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Testing |
| **Öncelik** | Medium |
| **Çaba** | Large (~6 saat) |
| **Bağımlılık** | Yok |

**Eksik:**
- 42 sayfa, yalnızca 2 Playwright E2E spec'i
- React Testing Library veya Vitest ile birim test yok
- API client katmanı (`lib/api.ts`) test edilmemiş

**Öneri:**
1. `vitest` + `@testing-library/react` kurulumu
2. API client birim testleri (1744 satır, en kritik modül)
3. Login sayfası + Dashboard için ilk komponent testleri
4. Mevcut Playwright E2E spec'lerini genişletme

### 2.4 Benchmark CI İzleme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Testing |
| **Öncelik** | Low |
| **Çaba** | Medium (~2 saat) |
| **Bağımlılık** | Yok |

Benchmark'lar mevcut (`test/bench/bench_test.go`) ancak CI'da regresyon izlenmiyor. `benchstat` veya `golang.org/x/perf` ile CI'a benchmark karşılaştırması eklenebilir.

### 2.5 Kapsama Test Dosyalarını Gözden Geçirme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Architecture & Design |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

~31 coverage-specific test dosyası (`admin_coverage*.go`, `coverpush_*.go`) var. Bunlar:
- Gerçek test senaryolarını mı yoksa sadece branch kapsamını mı exercise ediyor?
- Anlamlı integration test'lere dönüştürülebilir mi?
- Gereksiz olanlar silinebilir mi?

---

## 5. Faz 3: Ön Yüz & Erişilebilirlik (1-2 hafta)

### 3.1 Erişilebilirlik Denetimi (WCAG 2.2 AA)

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Dashboard/UI |
| **Öncelik** | Medium |
| **Çaba** | XL (~16 saat) |
| **Bağımlılık** | Yok |

**Denetlenecek alanlar:**
1. **Ekran okuyucu uyumluluğu** — aria-label, role, live region eksikleri
2. **Klavye navigasyonu** — tüm interactive öğeler tab sırasında, focus-visible stilleri
3. **Renk kontrastı** — mevcut tema için 4.5:1 oranı kontrolü
4. **Hit target ≥44px** — mobil dokunmatik hedefler
5. **Form etiketleri** — her input'un ilişkili bir label'ı var mı?
6. **Sayfa başlıkları** — her sayfada unique `<title>`

**Araçlar:**
- `axe-core` (Playwright entegrasyonu ile)
- `Lighthouse` CI
- Manuel klavye testi

### 3.2 Hata Yönetimi Kullanıcı Deneyimi

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Dashboard/UI |
| **Öncelik** | Medium |
| **Çaba** | Large (~8 saat) |
| **Bağımlılık** | Yok |

**Mevcut durum:** `api.ts`'de birçok `.catch(() => {})` — sessiz hata yutma.

**Yapılacaklar:**
1. Tüm API çağrılarında kullanıcıya görünür hata bildirimi
2. Toast notification sistemi (başarı/hata/uyarı)
3. Network hatası durumunda yeniden bağlanma göstergesi
4. Süresi dolan oturum için otomatik login sayfasına yönlendirme
5. DebugLogDrawer'da hataların daha belirgin gösterimi

### 3.3 Yükleniyor Durumları (Loading Skeletons)

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Dashboard/UI |
| **Öncelik** | Low |
| **Çaba** | Medium (~4 saat) |
| **Bağımlılık** | 3.1 ile paralel |

Mevcut spinner (`PageLoader`) yerine skeleton loading pattern'leri:
- Dashboard istatistik kartları için skeleton
- Tablo satırları için skeleton
- Domain detay sayfası için skeleton

### 3.4 Mobil İyileştirmeler

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Dashboard/UI |
| **Öncelik** | Low |
| **Çaba** | Large (~6 saat) |
| **Bağımlılık** | 3.1 ile paralel |

**Kontrol edilecek sayfalar (özellikle geniş tablolar):**
- Domains listesi
- DNS zone editor
- Audit Log
- Analytics tabloları
- PHP listesi
- Certificates listesi

### 3.5 Bağlantı Hatası / Offline Durumu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Dashboard/UI |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | 3.2 ile bağlantılı |

API erişilemez olduğunda:
- Dashboard boş/stale veri göstermek yerine "Sunucuya bağlanılamadı" bildirimi
- Otomatik yeniden bağlanma göstergesi
- Son bilinen verinin görüntülenmesi (stale data pattern)

---

## 6. Faz 4: Mimari & Kod Kalitesi (2-3 hafta)

### 4.1 Admin API Bağımlılık Enjeksiyonu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Architecture & Design |
| **Öncelik** | Medium |
| **Çaba** | Large (~6 saat) |
| **Bağımlılık** | Yok |

Mevcut `admin.Server` ~15 subsystem'e setter metotlarıyla bağlanıyor:

```go
s.admin.SetCache(cacheEngine)
s.admin.SetReloadFunc(s.reload)
s.admin.SetAnalytics(s.analytics)
s.admin.SetAlerter(alerter)
s.admin.SetAuthManager(s.authMgr)
s.admin.SetTLSManager(s.tlsMgr)
// ...
```

**Öneri:** Tek bir `AdminDependencies` struct'ı:

```go
type AdminDependencies struct {
    Cache       *cache.Engine
    ReloadFunc  func() error
    Analytics   *analytics.Collector
    Alerter     *alerting.Alerter
    AuthMgr     *auth.Manager
    TLSManager  *uwastls.Manager
    // ...
}

func New(cfg *config.Config, log *logger.Logger, m *metrics.Collector, deps AdminDependencies) *Server
```

### 4.2 Server Struct Boyutu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Architecture & Design |
| **Öncelik** | Low |
| **Çaba** | Large (~8 saat) |
| **Bağımlılık** | 4.1 ile bağlantılı |

`Server` struct'ı ~50 field içeriyor. Alt sistemleri gruplamak için:

```go
type Server struct {
    // Core
    config   *config.Config
    logger   *logger.Logger
    // ...

    // Subsystem groups
    routing   *routingSubsystem    // vhosts, proxyPools, balancers, etc.
    security  *securitySubsystem   // ipACLGuards, wafGuards, rateLimiters, etc.
    observ    *observabilitySubsystem // metrics, analytics, domainLogs, etc.
}
```

### 4.3 Htaccess Çift Önbellek Birleştirme

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Architecture & Design |
| **Öncelik** | Low |
| **Çaba** | Medium (~2 saat) |
| **Bağımlılık** | Yok |

`htaccessCache` (v1) ve `htaccessCacheV2` paralel var. Migration tamamlandıysa v1 kaldırılmalı. Yorum satırı "gradual migration" diyor — migration durumu netleştirilmeli.

### 4.4 Kapsama Test Dosyalarını Birleştirme (2.5'in devamı)

| Özellik | Detay |
|:--------|:------|
| **Öncelik** | Low |
| **Çaba** | Medium (~4 saat) |
| **Bağımlılık** | 2.5 (faz 2'de analiz, faz 4'te uygulama) |

Analiz sonrası anlamlı integration test'lere dönüştürme.

### 4.5 API Anahtarını sessionStorage'dan httpOnly Cookie'ye Taşıma

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Security |
| **Öncelik** | Medium |
| **Çaba** | Large (~6 saat) |
| **Bağımlılık** | Yok |

Mevcut: API key `sessionStorage`'da saklanıyor. XSS durumunda çalınabilir.

**Seçenekler:**
1. **HttpOnly cookie** (arka uç değişikliği gerektirir): Admin API'nin `/api/v1/auth/login` yanıtında `Set-Cookie` dönmesi
2. **SameSite=Strict** + HttpOnly + Secure flag ile
3. **Geçici çözüm:** CSP'yi sıkılaştırarak XSS riskini minimize etmek

**Öneri:** Session token'ı httpOnly cookie'ye taşımak, API key'i sadece CLI/MCP kullanımına bırakmak.

### 4.6 Backups.tsx Gelişmiş Cron Arayüzü

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | Deep Scan Kanban → Code Quality |
| **Öncelik** | Low |
| **Çaba** | Small (~1 saat) |
| **Bağımlılık** | Yok |

`web/dashboard/src/pages/Backups.tsx:47`'de TODO: "CRON_PRESETS - TODO: add advanced cron schedule UI". Özel cron ifadesi girişi eklenmeli.

---

## 7. Faz 5: Dokümantasyon & Operasyon (1 hafta)

### 5.1 OpenAPI / Swagger Dokümantasyonu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Documentation |
| **Öncelik** | Medium |
| **Çaba** | XL (~16 saat) |
| **Bağımlılık** | Yok |

251 API rotası için OpenAPI 3.1 spec'i:

**Yaklaşım:**
1. **Manuel** — Go handler'lardan OpenAPI yorumları çıkarmak için `swaggo/swag` benzeri bir araç
2. **Otomatik** — Route listesinden (`routes.go`) temel şema oluşturup elle zenginleştirme
3. **Kademeli** — Önce kritik endpoint'ler (auth, domains, apps), sonra diğerleri

**Çıktı:**
- `docs/api/openapi.yaml` — OpenAPI 3.1 spec dosyası
- Swagger UI entegrasyonu (örn. `/_uwas/api/docs/`)
- `docs/api/` altındaki mevcut markdown dosyalarını güncelleme

### 5.2 Sorun Giderme Kılavuzu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Documentation |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

`docs/troubleshooting.md` — sık karşılaşılan sorunlar:
- Dashboard'a bağlanamama
- Sertifika hataları
- PHP-FPM başlatılamaması
- Docker compose sorunları
- Migration hataları

### 5.3 Güvenlik Modeli Dokümantasyonu

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Documentation |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

`docs/SECURITY.md` — mevcut güvenlik mimarisini tek bir dokümanda toplamak:
- RBAC modeli (roller, izinler, endpoint koruması)
- TOTP 2FA akışı
- WAF kuralları ve false positive stratejisi
- SSRF koruma modeli
- Brute-force koruma katmanları

### 5.4 Derinlemesine Sağlık Kontrolü

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Operational |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

Mevcut `/healthz` sadece `{"status":"ok"}` döndürüyor. Daha derin bir kontrol:

```json
{
  "status": "ok",
  "uptime": "72h",
  "cache": { "status": "ok", "entries": 1234 },
  "php": { "versions": ["8.3", "8.4"], "running": 2 },
  "tls": { "cert_count": 5, "expiring_soon": 0 },
  "admin": { "enabled": true },
  "version": "v0.8.9"
}
```

### 5.5 Yapılandırma Migrasyon Aracı

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Operational |
| **Öncelik** | Low |
| **Çaba** | Medium (~3 saat) |
| **Bağımlılık** | Yok |

`uwas config migrate v0.7-to-v0.8` gibi bir CLI komutu. Mevcut `UPGRADING.md`'deki değişiklikleri otomatikleştirme.

### 5.6 Structured Logging İyileştirmesi

| Özellik | Detay |
|:--------|:------|
| **Kaynak** | COMPREHENSIVE-REVIEW.md §15 — Operational |
| **Öncelik** | Low |
| **Çaba** | Medium (~2 saat) |
| **Bağımlılık** | Yok |

Logger şu an ad-hoc string key-value çiftleri kullanıyor. Daha tutarlı bir structured logging için:
- Log field key'leri için sabitler (örn. `log.KeyError`, `log.KeyDuration`)
- Request-scoped logger (request ID otomatik eklenir)
- Audit log için ayrı structured logger

---

## 8. Bağımlılık Grafiği

```
Faz 1 (Hızlı Kazanımlar)
  ├── 1.1 Staticcheck      ──→ hiçbir şeye bağlı değil
  ├── 1.2 gofmt            ──→ hiçbir şeye bağlı değil
  ├── 1.3 Magic Numbers    ──→ hiçbir şeye bağlı değil
  ├── 1.4 CSP              ──→ hiçbir şeye bağlı değil
  ├── 1.5 TOTP Docs        ──→ hiçbir şeye bağlı değil
  └── 1.6 Bcrypt Cost      ──→ hiçbir şeye bağlı değil

Faz 2 (Test & Kalite)
  ├── 2.1 Zero Coverage    ──→ hiçbir şeye bağlı değil
  ├── 2.2 High Impact Cov  ──→ 2.1 (test altyapısı)
  ├── 2.3 Frontend Tests   ──→ hiçbir şeye bağlı değil
  ├── 2.4 Benchmarks CI    ──→ hiçbir şeye bağlı değil
  └── 2.5 Coverage Files   ──→ hiçbir şeye bağlı değil

Faz 3 (Ön Yüz)
  ├── 3.1 Accessibility     ──→ hiçbir şeye bağlı değil
  ├── 3.2 Error UX          ──→ hiçbir şeye bağlı değil
  ├── 3.3 Loading States    ──→ 3.1 ile paralel
  ├── 3.4 Mobile            ──→ 3.1 ile paralel
  └── 3.5 Offline State     ──→ 3.2 ile bağlantılı

Faz 4 (Mimari)
  ├── 4.1 Admin DI          ──→ hiçbir şeye bağlı değil
  ├── 4.2 Server Struct     ──→ 4.1 (aynı yaklaşım)
  ├── 4.3 Htaccess Cache    ──→ hiçbir şeye bağlı değil
  ├── 4.4 Coverage Consolid.─→ 2.5 (analiz tamamlanmalı)
  ├── 4.5 HttpOnly Cookie   ──→ hiçbir şeye bağlı değil
  └── 4.6 Cron UI           ──→ hiçbir şeye bağlı değil

Faz 5 (Dokümantasyon)
  ├── 5.1 OpenAPI           ──→ hiçbir şeye bağlı değil (büyük iş)
  ├── 5.2 Troubleshooting   ──→ hiçbir şeye bağlı değil
  ├── 5.3 Security Model    ──→ hiçbir şeye bağlı değil
  ├── 5.4 Health Check      ──→ hiçbir şeye bağlı değil
  ├── 5.5 Config Migration  ──→ hiçbir şeye bağlı değil
  └── 5.6 Structured Logs   ──→ hiçbir şeye bağlı değil
```

**Kritik yol:** Her faz bağımsız — ancak Faz 2 (test), Faz 3 (ön yüz) ve Faz 4 (mimari) paralel yürütülebilir. Faz 5 (dokümantasyon) diğerlerinden bağımsız.

---

## 9. Mevcut Kanban ile İlişki

### Deep Scan Results Board'u ile Eşleme

| Board'daki Sütun | Oradaki Maddeler | Bu Plana Yansıması |
|:-----------------|:-----------------|:-------------------|
| 📋 Coverage Gaps (<80%) | 14 madde (kapsama boşlukları) | **Faz 2.1 + 2.2** — tamamen kapsanıyor |
| 🔧 Code Quality & Lint | 7 madde (lint + gofmt + todo) | **Faz 1.1 + 1.2 + 4.6** |
| 🔒 Security Observations | Boş | **Faz 1.4 + 1.5 + 1.6 + 4.5** yeni maddeler |
| 📦 Documentation Gaps | Boş | **Faz 5.1 + 5.2 + 5.3** yeni maddeler |
| ✅ Verified Clean | 7 madde (mevcut durum) | Referans olarak tutuluyor |

### Coverage Improvement Goals Board'u ile Eşleme

| Sütun | Oradaki Maddeler | Durum |
|:------|:-----------------|:------|
| 🎯 Target <70% | 7 madde | **Faz 2.1 + 2.2** — kapsanıyor |
| ✅ Complete | 5 madde (isAdmin, Cloudflare, Apps, PHP, Preflight) | Zaten çözülmüş ✅ |

**Yeni board önerisi:** Bu plandaki Faz 3-5 maddeleri için yeni bir kanban board'u oluşturulabilir: "UWAS İyileştirme Planı — Faz 3/4/5"

---

## 10. Risk Değerlendirmesi

| Risk | Olasılık | Etki | Mitigasyon |
|:-----|:---------|:-----|:-----------|
| Test aralığı genişletme mevcut kapsamayı düşürebilir | Düşük | Orta | Yeni testler ek kapsama sağlar, düşmez |
| Mimari değişiklikler (DI, Server struct) regresyona yol açabilir | Orta | Yüksek | Her değişiklikten sonra tam test süiti; kademeli geçiş |
| Ön yüz yenilikleri mevcut özellikleri bozabilir | Düşük | Orta | Playwright E2E testleri ile koruma |
| OpenAPI dokümantasyonu 251 rota için çok zaman alabilir | Yüksek | Düşük | Kademeli yaklaşım: önce kritik endpoint'ler |
| Kaynak kısıtı (tek geliştirici) | Orta | Yüksek | Önceliklendirme: en yüksek etkiye sahip maddeler önce |

---

## 11. Metrikler & Başarı Kriterleri

### Faz 1 (Hızlı Kazanımlar)

| Metrik | Hedef |
|:-------|:------|
| Staticcheck uyarı sayısı | 4 → **0** |
| gofmt uyarılı dosya sayısı | 10+ → **0** |
| Magic number dağınıklığı | Dosyalara yayılmış → **merkezi constant** |

### Faz 2 (Test Altyapısı)

| Metrik | Hedef |
|:-------|:------|
| 0% kapsama fonksiyon sayısı | 6 → **0** |
| <70% kapsama fonksiyon sayısı | 32 → **<10** |
| Frontend test coverage | 0% → **>20%** |
| CI benchmark regresyon izleme | Yok → **Var** |

### Faz 3 (Ön Yüz)

| Metrik | Hedef |
|:-------|:------|
| WCAG 2.2 AA uyumluluk | Denetim yok → **raporlu** |
| Sessiz hata yutma noktası | ~20+ → **0** |
| Skeleton loading sayfa sayısı | 0 → **>10** |

### Faz 4 (Mimari)

| Metrik | Hedef |
|:-------|:------|
| Admin setter metot sayısı | ~15 → **1 (struct)** |
| Server struct field sayısı | ~50 → **~25** |
| Coverage test dosyası sayısı | ~31 → **~15** |

### Faz 5 (Dokümantasyon)

| Metrik | Hedef |
|:-------|:------|
| API rotaları dokümante | 0/251 → **251/251** |
| Troubleshooting dokümanı | Yok → **Var** |
| Security model dokümanı | Yok → **Var** |

### Genel Proje Metrikleri (Korunacak)

| Metrik | Mevcut | Hedef |
|:-------|:------:|:-----:|
| Test kapsamı | 90.9% | ≥90% (koru) |
| Test paketi geçişi | 55/55 | 55/55 (koru) |
| `go vet` | Temiz | Temiz (koru) |
| `staticcheck` | 4 uyarı | 0 uyarı |
| `go test -race` | 0 race | 0 race (koru) |
| Security risk skoru | 2.1 | ≤2.1 (koru) |
| Binary boyutu | ~15 MB | ~15 MB (koru) |

---

## 12. Önerilen Sprint Planlaması

### Sprint 1: "Quick Wins" (2 gün)

```
Faz 1 tamamı:
  □ 1.1 Staticcheck (1h)
  □ 1.2 gofmt (30m)
  □ 1.3 Magic Numbers (1h)
  □ 1.4 CSP (3h)
  □ 1.5 TOTP Docs (15m)
  □ 1.6 Bcrypt Cost (15m)
```

### Sprint 2: "Test Coverage — Kritik" (1 hafta)

```
Faz 2:
  □ 2.1 Zero Coverage Functions (6-8h)
  □ 2.2 High Impact Gaps (16h)          ← en büyük iş
  □ 2.5 Coverage Files Review (3h)
```

### Sprint 3: "Frontend Foundations" (1 hafta)

```
Faz 3'ün bir kısmı + Faz 2'nin kalanı:
  □ 2.3 Frontend Tests (6h)
  □ 2.4 Benchmark CI (2h)
  □ 3.1 Accessibility Audit (4h — ilk tarama)
  □ 3.2 Error UX (4h — ilk adımlar)
```

### Sprint 4: "Frontend Deep Dive" (1 hafta)

```
Faz 3 devam:
  □ 3.1 Accessibility Fixes (12h)
  □ 3.2 Error UX Completion (4h)
  □ 3.3 Loading States (4h)
  □ 3.5 Offline State (3h)
```

### Sprint 5: "Architecture" (1 hafta)

```
Faz 4:
  □ 4.1 Admin DI (6h)
  □ 4.5 HttpOnly Cookie (6h)
  □ 4.6 Cron UI (1h)
```

### Sprint 6: "Architecture Deep" (1 hafta)

```
Faz 4 devam + Faz 5 başlangıç:
  □ 4.2 Server Struct (8h)              ← 4.1 sonrası
  □ 4.3 Htaccess Cache (2h)
  □ 4.4 Coverage Consolidation (4h)     ← 2.5 sonrası
  □ 5.4 Health Check (3h)
```

### Sprint 7: "Docs & Polish" (1 hafta)

```
Faz 5:
  □ 5.1 OpenAPI — kritik rotalar (8h)
  □ 5.2 Troubleshooting (3h)
  □ 5.3 Security Model (3h)
  □ 5.5 Config Migration (3h)
  □ 5.6 Structured Logging (2h)
```

### Sprint 8: "Documentation Completion" (1 hafta)

```
Faz 5 devam:
  □ 5.1 OpenAPI — kalan rotalar (8h)
  □ Final review + metrik raporu
```

---

## Ek: 25 Maddenin Hızlı Referans Kartı

| # | Madde | Faz | Öncelik | Çaba | Tür |
|:---|:------|:---:|:-------:|:----:|:----|
| 1 | Staticcheck uyarıları | 1 | High | Small | Code Quality |
| 2 | gofmt formatlama | 1 | Med | Small | Code Quality |
| 3 | Sihirli sayılar | 1 | Med | Small | Code Quality |
| 4 | CSP unsafe-inline | 1 | Low | Med | Security |
| 5 | TOTP dokümantasyonu | 1 | Low | Small | Security |
| 6 | Bcrypt cost yükseltme | 1 | Low | Small | Security |
| 7 | Sıfır kapsama fonksiyonlar | 2 | **High** | Large | Testing |
| 8 | Yüksek etkili kapsama boşlukları | 2 | **High** | Large | Testing |
| 9 | Frontend test altyapısı | 2 | Med | Large | Testing |
| 10 | Benchmark CI | 2 | Low | Med | Testing |
| 11 | Kapsama dosyalarını inceleme | 2 | Low | Med | Testing |
| 12 | Erişilebilirlik denetimi | 3 | Med | **XL** | UI/UX |
| 13 | Hata yönetimi UX | 3 | Med | Large | UI/UX |
| 14 | Loading skeleton | 3 | Low | Med | UI/UX |
| 15 | Mobil iyileştirmeler | 3 | Low | Large | UI/UX |
| 16 | Offline/bağlantı hatası | 3 | Low | Med | UI/UX |
| 17 | Admin DI refaktörü | 4 | Med | Large | Architecture |
| 18 | Server struct boyutu | 4 | Low | Large | Architecture |
| 19 | Htaccess çift önbellek | 4 | Low | Med | Architecture |
| 20 | Kapsama dosyalarını birleştirme | 4 | Low | Med | Architecture |
| 21 | HttpOnly cookie | 4 | Med | Large | Security |
| 22 | Cron arayüzü TODO | 4 | Low | Small | UI/UX |
| 23 | OpenAPI dokümantasyonu | 5 | Med | **XL** | Docs |
| 24 | Troubleshooting kılavuzu | 5 | Low | Med | Docs |
| 25 | Güvenlik modeli dokümanı | 5 | Low | Med | Docs |
| 26 | Derin sağlık kontrolü | 5 | Low | Med | Operational |
| 27 | Config migrasyon aracı | 5 | Low | Med | Operational |
| 28 | Structured logging | 5 | Low | Med | Operational |

---

*Plan, COMPREHENSIVE-REVIEW.md (2026-07-15) temel alınarak hazırlanmıştır.  
Mevcut kanban board'larındaki coverage maddeleri ile tam uyumludur.*
