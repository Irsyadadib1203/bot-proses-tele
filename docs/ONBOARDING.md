# 🚀 Panduan Onboarding Klien Baru (Setup Checklist)

Dokumen ini adalah panduan langkah-demi-langkah bagi pengembang/klien baru untuk mengonfigurasi dan mengaktifkan bot pembelian massal (bulk order) ini.

---

## 📋 Checklist Langkah Setup

- [ ] **Langkah 1: Clone Repository & Buat Salinan Konfigurasi**
- [ ] **Langkah 2: Buat Bot Telegram & Dapatkan Token**
- [ ] **Langkah 3: Dapatkan Telegram User ID & Atur Whitelist**
- [ ] **Langkah 4: Sesuaikan Adapter Provider Target (`internal/adapter`)**
- [ ] **Langkah 5: Konfigurasi Mapping Produk & Batasan di `config.yaml`**
- [ ] **Langkah 6: Siapkan Environment Variables (`.env`)**
- [ ] **Langkah 7: Jalankan Aplikasi (Database Auto-Migrate)**
- [ ] **Langkah 8: Uji Coba Transaksi & Verifikasi Rekap**

---

### Langkah 1: Clone Repository & Buat Salinan Konfigurasi

1. Clone template repository ke direktori kerja baru:
   ```bash
   git clone <repo-url> bot-klien-xyz
   cd bot-klien-xyz
   ```
2. Buat file `.env` dari template `.env.example`:
   ```bash
   cp .env.example .env
   ```

---

### Langkah 2: Buat Bot Telegram & Dapatkan Token

1. Buka Telegram dan cari **@BotFather**.
2. Kirim perintah `/newbot` dan ikuti instruksi pembuatan nama bot.
3. Simpan token bot yang diberikan (format: `123456789:ABCdefGHIjklMNOpqrSTUvwxYZ`).
4. Buka `.env` dan masukkan ke variabel `TELEGRAM_BOT_TOKEN`:
   ```env
   TELEGRAM_BOT_TOKEN=123456789:ABCdefGHIjklMNOpqrSTUvwxYZ
   ```

---

### Langkah 3: Dapatkan Telegram User ID & Atur Whitelist

Bot ini dilengkapi pengaman **Whitelist Telegram User ID**. User di luar whitelist akan langsung ditolak.

1. Buka Telegram dan cari bot pencari ID seperti **@userinfobot** atau kirim pesan ke bot Anda setelah jalan.
2. Catat User ID numerik akun Anda (contoh: `1267876327`).
3. Masukkan ke `.env` (bisa beberapa ID dipisah koma):
   ```env
   ALLOWED_TELEGRAM_USER_IDS=1267876327,987654321
   ```

---

### Langkah 4: Sesuaikan Adapter Provider Target (`internal/adapter/`)

> [!IMPORTANT]
> **HANYA file di folder `internal/adapter/` yang perlu diubah.**
> Folder `internal/core/`, `internal/store/`, dan `internal/telegram/` **TIDAK PERLU** dimodifikasi.

Buka [default_adapter.go](file:///d:/Pribadi/bot-proses/internal/adapter/default_adapter.go) dan sesuaikan dua method berikut:

#### 1. Method `PlaceOrder(ctx, req)`
- **Idempotency Key:** Parameter `req.IdempotencyKey` berisi format unik deterministik `<batch_id>-<sequence_no>` (misal `105-1`, `105-2`).
- **WAJIB:** Jika API provider Anda mendukung parameter `ref_id` / `client_trx_id` / `idempotency_key` (seperti Digiflazz, VIP Reseller, dll.), **wajib sertakan** nilai `req.IdempotencyKey` ini dalam request body / header ke API provider untuk mencegah transaksi ganda!
- Kembalikan struct `core.OrderResult`:
  - `SN`: Serial number dari provider (voucher code / token PLN / SN transaksi).
  - `ProviderRef`: ID transaksi di sisi provider.
  - `Status`: `core.OrderStatusSuccess` ("success") atau `core.OrderStatusFailed` ("failed").
  - `Message`: Pesan error dari provider jika gagal.

#### 2. Method `CheckStatus(ctx, idempotencyKey)`
- Digunakan oleh fitur **Auto-Resume Recovery** saat service restart mendadak.
- **Kasus A (Provider mendukung cek status via ref_id):**
  Panggil endpoint status query provider Anda.
  Kembalikan: `(OrderResult{Status: "success", SN: "..."}, true, nil)`
- **Kasus B (Provider TIDAK mendukung cek status via ref_id):**
  Cukup kembalikan:
  ```go
  return core.OrderResult{}, false, nil
  ```
  *(Sistem secara aman akan menandai item tersebut sebagai `needs_manual_review` dan tidak akan melakukan double-order secara membabi-buta).*

---

### Langkah 5: Konfigurasi Mapping Produk di `config/config.yaml`

Buka [config.yaml](file:///d:/Pribadi/bot-proses/config/config.yaml) dan sesuaikan:

```yaml
# Mapping kode produk singkat di Telegram -> SKU/kode asli di API provider target
products:
  FF5: "free_fire_5_diamonds_provider_sku"
  FF50: "free_fire_50_diamonds_provider_sku"
  ML10: "mobile_legends_10_diamonds_sku"

worker:
  concurrency: 5        # Jumlah goroutine paralel (default: 5)
  max_qty_per_batch: 200 # Batas maksimum order per satu command BUY (default: 200)
  shutdown_timeout_sec: 30 # Waktu tunggu worker menyelesaikan order in-flight saat shutdown
```

---

### Langkah 6: Siapkan Database & Environment Variables (`.env`)

Isi konfigurasi database MySQL Anda di `.env`:

```env
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USER=root
DB_PASSWORD=your_password
DB_NAME=bulk_order_db

TARGET_BASE_URL=https://api.namaprovider.com/v1
TARGET_API_KEY=api_key_rahasia
TARGET_API_SECRET=api_secret_rahasia
```

---

### Langkah 7: Jalankan Aplikasi

#### Opsi A: Menggunakan Docker Compose (Direkomendasikan untuk Production)

```bash
docker-compose up -d --build
```
*Docker compose otomatis menjalankan container MySQL, melakukan health check, menginisialisasi skema tabel otomatis, dan menjalankan aplikasi.*

#### Opsi B: Menjalankan Langsung via Go Binary

Pastikan MySQL sudah berjalan dan database `bulk_order_db` sudah dibuat:
```bash
go run ./cmd/bot
```
> [!NOTE]
> **Database Migrations:** Dijalankan secara **otomatis** oleh binary aplikasi saat startup menggunakan library `golang-migrate` tanpa perlu instalasi CLI tambahan!

---

### Langkah 8: Uji Coba Transaksi

1. Buka bot di Telegram, kirim:
   ```
   /start
   ```
2. Kirim perintah pembelian massal:
   ```
   BUY FF5 1267876327 10
   ```
3. Bot akan membalas langsung:
   `⏳ Batch #1 diterima, memproses 10 order...`
4. Di background, worker pool memproses secara terkontrol (sesuai `concurrency`).
5. Setelah selesai, bot mengirimkan dokumen rekap file `.csv` beserta ringkasan status (Sukses/Gagal).
