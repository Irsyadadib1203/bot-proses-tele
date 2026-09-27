# 🤖 Bulk Order Telegram Bot Template (Golang)

Template project Telegram Bot berbasis Go untuk pemrosesan pembelian produk digital massal (*bulk order*) secara asinkron dengan worker pool concurrency terkontrol, idempotency key per item, auto-resume saat restart, dan ekspor rekap otomatis.

---

## 🏗️ Arsitektur Proyek

Sistem ini didesain dengan pemisahan tegas antara **Core Logic** (generik, tidak diubah) dan **Adapter Layer** (spesifik klien):

```
├── cmd/
│   └── bot/
│       └── main.go                 # Entry point, auto-migrations, graceful shutdown
├── config/
│   ├── config.go                   # Config struct & loader
│   ├── config.yaml                 # Konfigurasi produk & worker
│   └── .env.example                # Template credentials & secret
├── docs/
│   └── ONBOARDING.md               # Checklist setup langkah-demi-langkah untuk klien baru
├── internal/
│   ├── adapter/                    # [KHUSUS KLIEN] Implementasi PlaceOrder & CheckStatus
│   │   └── default_adapter.go
│   ├── core/                       # [GENERIK] Parser, Worker Pool, Resume Manager, CSV Export
│   │   ├── adapter.go
│   │   ├── export.go
│   │   ├── orchestrator.go
│   │   ├── parser.go
│   │   ├── ratelimit.go
│   │   └── resume.go
│   ├── store/                      # [DATABASE] MySQL repository & models
│   │   ├── models.go
│   │   ├── mysql.go
│   │   └── repository.go
│   └── telegram/                   # [TELEGRAM] Bot handlers, validation, document sender
│       ├── bot.go
│       └── handlers.go
├── migrations/                     # SQL migration schema (Auto-applied by embedded binary)
│   ├── 000001_init_schema.up.sql
│   └── 000001_init_schema.down.sql
├── Dockerfile
├── docker-compose.yml
├── go.mod
└── go.sum
```

---

## ⚡ Fitur Utama

1. **Format Perintah Sederhana & Ketat:**
   ```
   BUY <KODE_PRODUK> <TARGET_ID> <QTY>
   ```
   *Contoh:* `BUY FF5 1267876327 100`

2. **Asynchronous & Non-Blocking:**
   Bot membalas instan dengan nomor ID Batch, lalu memproses antrian order di background goroutine.

3. **Worker Pool Ber-Concurrency Terbatas:**
   Concurrency dapat diatur (default: 5 worker parallel) dengan panic recovery per worker goroutine.

4. **Idempotency Key Deterministik & Pencegahan Double-Order:**
   - Status item memiliki 4 tahap: `pending` ➔ `in_progress` (di-commit ke DB sebelum memanggil API provider) ➔ `success` / `failed`.
   - Tiap item memiliki `IdempotencyKey` deterministik: `<batch_id>-<sequence_no>`.

5. **Auto-Resume Recovery on Restart / Crash:**
   - Item `pending` otomatis diproses ulang.
   - Item `in_progress` diverifikasi via `adapter.CheckStatus(ctx, idempotencyKey)`. Jika provider tidak mendukung pengecekan status, item ditandai `needs_manual_review` agar tidak terjadi transaksi dobel.

6. **Rekap Otomatis via Dokumen CSV:**
   Setelah semua item selesai, bot otomatis mengirim file `.csv` hasil transaksi (nomor urut, SN, provider ref, status, pesan error) dan rangkuman di caption Telegram.

7. **Graceful Shutdown:**
   Menunggu worker yang sedang aktif (`in_progress`) menyelesaikan pemanggilan API dan menyimpan data ke database sebelum mematikan aplikasi.

8. **Auto Database Migration:**
   Menggunakan `golang-migrate` embedded dalam binary Go, skema tabel langsung dibuat otomatis saat pertama kali dijalankan.

---

## 🚀 Cara Menjalankan

### 1. Salin Environment
```bash
cp .env.example .env
```
Isi `TELEGRAM_BOT_TOKEN`, `ALLOWED_TELEGRAM_USER_IDS`, dan koneksi database MySQL di file `.env`.

### 2. Jalankan dengan Docker Compose (Rekomendasi)
```bash
docker-compose up -d --build
```

### 3. Jalankan Secara Lokal
```bash
# Jalankan unit test
go test -v ./...

# Jalankan bot
go run ./cmd/bot
```

---

## 📖 Panduan Onboarding Klien Baru

Untuk integrasi ke provider baru, silakan baca dokumentasi lengkap di:
👉 **[docs/ONBOARDING.md](docs/ONBOARDING.md)**
