# ClipForge

Potong video YouTube menjadi clip siap dibagikan, lewat antarmuka web sederhana.

ClipForge mengunduh video dengan `yt-dlp`, memotongnya sesuai waktu yang dipilih, mengubah ukurannya sesuai aspect ratio, lalu mengemas hasilnya sebagai file MP4 (H.264 + AAC) yang bisa langsung diunduh.

## Fitur

- Input berupa URL YouTube (tautan `watch`, `youtu.be`, `shorts`, maupun `embed`).
- Waktu mulai dan selesai dalam format `MM:SS` atau `HH:MM:SS`.
- Durasi clip maksimal 10 menit.
- Aspect ratio: `9:16`, `16:9`, `1:1`.
- Kualitas: `1080p`, `720p`, `480p`.
- Hasil berupa tautan unduhan MP4.
- Potong otomatis jadi beberapa clip dari satu video (lihat
  "Potong otomatis jadi beberapa clip" di bawah).
- Mode viral: cari momen berpotensi viral dari transkrip video.
- Subtitle otomatis yang dibakar ke video, diambil dari caption
  YouTube bila tersedia.

## Tutorial instalasi

Ikuti langkah-langkah berikut secara berurutan. Tutorial ini ditulis
untuk Windows; pengguna Linux/macOS bisa memakai cara yang setara
(misalnya `apt`, `brew`, atau unduhan biner resmi masing-masing alat).

### Langkah 1 — Pasang Node.js

1. Unduh installer Node.js versi LTS dari https://nodejs.org/.
2. Jalankan installer dan ikuti sampai selesai (biarkan opsi bawaan).
3. Buka terminal baru, lalu verifikasi:

```powershell
node -v
npm -v
```

Keduanya harus menampilkan nomor versi. Bila tidak dikenali, tutup
terminal dan buka lagi, atau pasang ulang Node.js.

### Langkah 2 — Pasang FFmpeg

Cara termudah, bila `winget` tersedia di komputermu:

```powershell
winget install Gyan.FFmpeg
```

Bila `winget` tidak dikenali, pasang manual:

1. Unduh [file zip essentials FFmpeg dari Gyan](https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip).
2. Ekstrak. Di dalamnya ada folder `bin` berisi `ffmpeg.exe` dan
   `ffprobe.exe`.
3. Buat folder `C:\tools`, lalu salin `ffmpeg.exe` dan `ffprobe.exe`
   ke sana.
4. Tambahkan folder itu ke PATH dengan menjalankan perintah berikut
   di PowerShell:

```powershell
[Environment]::SetEnvironmentVariable("Path", $env:Path + ";C:\tools", "User")
```

### Langkah 3 — Pasang yt-dlp

Cara termudah, bila `winget` tersedia:

```powershell
winget install yt-dlp.yt-dlp
```

Bila `winget` tidak dikenali, pasang manual:

1. Unduh [yt-dlp.exe](https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe).
2. Salin `yt-dlp.exe` ke folder yang sama, yaitu `C:\tools`
   (folder yang sudah ditambahkan ke PATH pada langkah 2).

### Langkah 4 — Verifikasi FFmpeg dan yt-dlp

Langkah ini wajib, dan harus dilakukan di **terminal baru** — PATH yang
baru ditambahkan hanya terbaca oleh terminal yang dibuka setelah
pemasangan selesai. Tanpa FFmpeg dan yt-dlp, proses clip akan gagal
dengan pesan "Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah
terpasang."

```powershell
ffmpeg -version
yt-dlp --version
```

Keduanya harus menampilkan nomor versi. Bila masih muncul pesan
"not recognized", ulangi langkah 2–3 dan pastikan terminal yang dipakai
adalah terminal baru.

### Langkah 5 — Clone dan jalankan ClipForge

```bash
git clone https://github.com/mulyana10223012-sudo/ClipForge.git
cd ClipForge
npm install
npm start
```

Tunggu sampai muncul tulisan `ClipForge berjalan di http://localhost:3000`,
lalu buka http://localhost:3000/ di browser. Jangan tutup terminal
`npm start` selama memakai ClipForge, karena menutupnya akan
menghentikan server.

Bila port 3000 sudah dipakai aplikasi lain, jalankan dengan port
berbeda, misalnya:

```bash
# Linux/macOS
PORT=3001 npm start

# Windows (Command Prompt)
set PORT=3001 && npm start

# Windows (PowerShell)
$env:PORT=3001; npm start
```

## Cara memakai

1. Tempel URL YouTube pada kolom URL.
2. Isi waktu mulai dan selesai, misalnya `00:10` sampai `00:40`.
3. Pilih aspect ratio dan kualitas.
4. Klik **Buat Clip**, tunggu sampai selesai, lalu klik tautan unduhan
   yang muncul.

## Potong otomatis jadi beberapa clip

Video berdurasi panjang (misalnya 1 jam) tidak perlu dipotong manual
satu per satu. Pilih mode **Beberapa clip otomatis** di halaman web,
lalu isi:

- **Mulai** — titik awal pengambilan clip, misalnya `00:00`.
- **Durasi per clip** — panjang tiap clip dalam detik (5–600).
- **Jumlah clip** — banyaknya clip yang dibuat (1–20).
- **Jeda antar clip** — jarak antar clip dalam detik. Jeda inilah yang
  membuat hasilnya tidak monoton: dengan jeda, clip diambil dari
  berbagai titik di sepanjang video, bukan melulu dari awal video.

Contoh: mulai `00:00`, durasi 30 detik, jumlah 5 clip, jeda 300 detik
(5 menit) menghasilkan clip pada `00:00–00:30`, `05:30–06:00`,
`11:00–11:30`, `16:30–17:00`, dan `22:00–22:30`. Video hanya diunduh
sekali, lalu dipotong berkali-kali dari file yang sama, sehingga
jauh lebih hemat daripada mengunduh ulang video 1 jam untuk tiap clip.

Endpoint yang dipakai mode ini adalah `POST /api/clips`:

```json
{
  "videoId": "dQw4w9WgXcQ",
  "start": "00:00",
  "clipDuration": 30,
  "count": 5,
  "gap": 300,
  "aspectRatio": "9:16",
  "quality": "720p"
}
```

Responsnya berupa daftar clip beserta waktu dan tautan unduhannya:

```json
{
  "clips": [
    { "index": 1, "start": "00:00", "end": "00:30", "downloadUrl": "/downloads/<id>-1.mp4" },
    { "index": 2, "start": "05:30", "end": "06:00", "downloadUrl": "/downloads/<id>-2.mp4" }
  ]
}
```

## Cari momen viral

Pilih mode **Cari momen viral** untuk membiarkan server mencari
sendiri segmen-segmen yang paling berpotensi viral, tanpa harus
menebak-nebak timestamp manual.

Cara kerjanya:

1. Server mengunduh caption video saja dulu (tanpa mengunduh video),
   dari caption manual bila ada, kalau tidak ada dari caption otomatis
   YouTube, bahasa Indonesia dulu lalu bahasa Inggris.
2. Transkrip dibagi menjadi segmen-segmen sepanjang durasi clip yang
   dipilih (bawaan 30 detik, bisa diubah).
3. Tiap segmen diberi skor heuristik berdasarkan isi ucapannya:
   kata kunci hook dan kejutan (misalnya `rahasia`, `ternyata`,
   `tahukah`, `jangan`, `gratis`, `terbukti`), pertanyaan (`?`),
   angka dan persen, serta kepadatan ucapan. Segmen yang hanya berisi
   musik dinilai rendah.
4. Segmen dengan skor tertinggi dipilih (dengan jeda minimal satu
   segmen antar pilihan, supaya hasilnya tersebar, tidak berdekatan),
   lalu video diunduh sekali dan hanya segmen terpilih yang dipotong.
5. Hasilnya ditampilkan beserta skor dan kata kunci yang terdeteksi
   di tiap clip, misalnya `Clip 1 (12:34–13:04) • Skor 32 • Kata kunci:
   kenapa, gratis, terbukti`.

Batasan yang perlu dipahami:

- **Skor ini heuristik, bukan jaminan viral.** Tidak ada alat yang
  bisa memastikan sebuah clip akan viral — skor hanya menandai
  segmen yang secara pola kalimat mirip dengan pembuka yang biasa
  menarik perhatian.
- **Video tanpa caption tidak bisa dianalisis.** Bila video tidak
  punya caption manual maupun otomatis, server akan menolak dengan
  pesan yang jelas; untuk video seperti itu gunakan mode Beberapa
  clip otomatis.
- Mode Hook saja dari versi sebelumnya sudah dihapus dari tampilan
  dan diganti mode ini, sesuai permintaan.


## Subtitle otomatis

Centang **Tambah subtitle otomatis** pada mode apa pun (satu clip,
beberapa clip, maupun hook). Server akan mengunduh caption video
dari YouTube — caption manual bila ada, kalau tidak ada maka caption
otomatis YouTube — dalam bahasa Indonesia dulu, lalu bahasa Inggris
bila tidak ada yang Indonesia. Subtitle tersebut dipotong dan
digeser waktunya agar pas dengan clip, lalu dibakar langsung ke
video (teks putih dengan outline hitam, di area bawah video),
sehingga terlihat di semua pemutar dan platform, bukan subtitle
terpisah yang bisa hilang.

Batasan penting:

- Bila video tidak punya caption manual maupun otomatis sama sekali,
  clip tetap dibuat, tetapi tanpa subtitle. Respons API akan
  mengembalikan `subtitled: false` dan catatan tersebut.
- Subtitle mengikuti isi caption YouTube apa adanya, termasuk bila
  caption otomatisnya kurang akurat.
- Untuk mode beberapa clip, subtitle tiap clip dipotong dari caption
  asli sesuai rentang waktunya masing-masing.

## API

### `POST /api/clip`

```json
{
  "videoId": "dQw4w9WgXcQ",
  "start": "00:10",
  "end": "00:40",
  "aspectRatio": "9:16",
  "quality": "720p"
}
```

Respons sukses:

```json
{
  "downloadUrl": "/downloads/<id>.mp4"
}
```

Hasil clip dilayani lewat `GET /downloads/<nama-file>.mp4`.

## Struktur proyek

```
server.js           # Server Express + endpoint /api/clip
public/
  index.html        # Antarmuka web
downloads/          # Hasil clip (tidak ikut commit, lihat .gitignore)
temp/               # Folder kerja sementara saat memproses video
```

## Pemecahan masalah

| Gejala | Kemungkinan penyebab / solusi |
| --- | --- |
| `Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah terpasang.` | FFmpeg atau yt-dlp belum terpasang, atau belum masuk PATH. Pasang keduanya, buka terminal baru, verifikasi dengan `ffmpeg -version` dan `yt-dlp --version`, lalu restart server (`Ctrl+C`, `npm start` lagi). |
| Halaman tidak terbuka di `localhost:3000` | Coba `http://127.0.0.1:3000/`, dan pastikan terminal `npm start` masih berjalan. |
| Port 3000 sudah dipakai | Jalankan dengan `PORT` lain seperti contoh di atas. |
