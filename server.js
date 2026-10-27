const express = require('express');
const path = require('path');
const fs = require('fs/promises');
const { existsSync } = require('fs');
const { execFile } = require('child_process');
const { promisify } = require('util');
const crypto = require('crypto');

const execFileAsync = promisify(execFile);
const app = express();
const PORT = Number(process.env.PORT) || 3000;
const ROOT = __dirname;
const PUBLIC_DIR = path.join(ROOT, 'public');
const DOWNLOADS_DIR = path.join(ROOT, 'downloads');
const TEMP_DIR = path.join(ROOT, 'temp');

app.use(express.json({ limit: '32kb' }));
app.use(express.static(PUBLIC_DIR));
app.use('/downloads', express.static(DOWNLOADS_DIR));

const QUALITY = { '1080p': '1080', '720p': '720', '480p': '480' };
const RATIOS = { '9:16': [9, 16], '16:9': [16, 9], '1:1': [1, 1] };

function parseTime(value) {
  if (typeof value !== 'string' || !/^\d{1,3}(?::\d{1,2}){0,2}$/.test(value.trim())) return NaN;
  const parts = value.trim().split(':').map(Number);
  if (parts.some((part) => !Number.isInteger(part) || part < 0 || part > 59)) return NaN;
  if (parts.length === 1) return parts[0];
  if (parts.length === 2) return parts[0] * 60 + parts[1];
  return parts[0] * 3600 + parts[1] * 60 + parts[2];
}

function fail(message, status = 400) {
  const error = new Error(message);
  error.status = status;
  throw error;
}

app.post('/api/clip', async (req, res) => {
  const { videoId, start, end, aspectRatio = '9:16', quality = '720p' } = req.body || {};
  const startSeconds = parseTime(start);
  const endSeconds = parseTime(end);

  try {
    if (!/^[A-Za-z0-9_-]{11}$/.test(String(videoId || ''))) fail('YouTube video ID tidak valid.');
    if (!Number.isFinite(startSeconds) || !Number.isFinite(endSeconds)) fail('Format waktu harus MM:SS atau HH:MM:SS.');
    if (endSeconds <= startSeconds) fail('Waktu akhir harus lebih besar daripada waktu mulai.');
    if (endSeconds - startSeconds > 600) fail('Durasi clip maksimal adalah 10 menit.');
    if (!RATIOS[aspectRatio]) fail('Aspect ratio tidak didukung.');
    if (!QUALITY[quality]) fail('Kualitas tidak didukung.');

    await Promise.all([fs.mkdir(DOWNLOADS_DIR, { recursive: true }), fs.mkdir(TEMP_DIR, { recursive: true })]);
    const jobId = crypto.randomUUID();
    const jobDir = path.join(TEMP_DIR, jobId);
    const source = path.join(jobDir, 'source.%(ext)s');
    const input = path.join(jobDir, 'source.mp4');
    const outputName = `${jobId}.mp4`;
    const output = path.join(DOWNLOADS_DIR, outputName);
    await fs.mkdir(jobDir, { recursive: true });

    try {
      await execFileAsync('yt-dlp', ['--no-playlist', '-f', `bestvideo[height<=${QUALITY[quality]}]+bestaudio/best[height<=${QUALITY[quality]}]`, '--merge-output-format', 'mp4', '-o', source, `https://www.youtube.com/watch?v=${videoId}`], { maxBuffer: 10 * 1024 * 1024 });
      const downloaded = (await fs.readdir(jobDir)).find((name) => name.startsWith('source.'));
      if (!downloaded) fail('Video tidak berhasil diunduh.', 502);
      const downloadedPath = path.join(jobDir, downloaded);
      if (downloadedPath !== input) await fs.rename(downloadedPath, input);
      const [rw, rh] = RATIOS[aspectRatio];
      const filter = `scale=if(gt(a\\,${rw}/${rh})\\,ih*${rw}/${rh}\\,iw):if(gt(a\\,${rw}/${rh})\\,ih\\,iw*${rh}/${rw}),crop=iw:ih,setsar=1`;
      await execFileAsync('ffmpeg', ['-y', '-ss', String(startSeconds), '-i', input, '-t', String(endSeconds - startSeconds), '-vf', filter, '-c:v', 'libx264', '-preset', 'fast', '-crf', '23', '-c:a', 'aac', '-b:a', '128k', '-movflags', '+faststart', output], { maxBuffer: 10 * 1024 * 1024 });
      res.json({ downloadUrl: `/downloads/${outputName}` });
    } finally {
      await fs.rm(jobDir, { recursive: true, force: true });
    }
  } catch (error) {
    const status = error.status || 500;
    console.error(error);
    res.status(status).json({ error: status === 500 ? 'Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah terpasang.' : error.message });
  }
});

app.get('*', (req, res) => res.sendFile(path.join(PUBLIC_DIR, 'index.html')));

Promise.all([fs.mkdir(DOWNLOADS_DIR, { recursive: true }), fs.mkdir(TEMP_DIR, { recursive: true })]).then(() => {
  app.listen(PORT, () => console.log(`ClipForge berjalan di http://localhost:${PORT}`));
}).catch((error) => { console.error(error); process.exit(1); });
