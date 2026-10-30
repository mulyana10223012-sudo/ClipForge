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


function formatTime(totalSeconds) {
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const mm = String(minutes).padStart(2, '0');
  const ss = String(seconds).padStart(2, '0');
  if (hours > 0) return `${hours}:${mm}:${ss}`;
  return `${mm}:${ss}`;
}


function parseSubtitleTimestamp(text) {
  const clean = text.trim();
  const [time, frac = '0'] = clean.split(/[,.]/);
  const parts = time.split(':').map(Number);
  if (parts.some((n) => !Number.isFinite(n))) return NaN;
  let seconds = 0;
  if (parts.length === 3) seconds = parts[0] * 3600 + parts[1] * 60 + parts[2];
  else if (parts.length === 2) seconds = parts[0] * 60 + parts[1];
  else return NaN;
  return seconds + Number(`0.${frac}`);
}
function formatSubtitleTimestamp(totalSeconds) {
  const ms = Math.max(0, Math.round(totalSeconds * 1000));
  const h = String(Math.floor(ms / 3600000)).padStart(2, '0');
  const mi = String(Math.floor((ms % 3600000) / 60000)).padStart(2, '0');
  const s = String(Math.floor((ms % 60000) / 1000)).padStart(2, '0');
  const m = String(ms % 1000).padStart(3, '0');
  return `${h}:${mi}:${s},${m}`;
}
// Potong + geser subtitle ke rentang clip. Entri di luar rentang dibuang,
// sisanya digeser agar mulai dari 0. Mengembalikan null bila tidak ada
// entri yang tumpang tindih dengan clip.
function sliceSubtitles(content, segmentStart, duration) {
  const segmentEnd = segmentStart + duration;
  const blocks = content.replace(/\r\n/g, '\n').split(/\n\s*\n/);
  const out = [];
  for (const block of blocks) {
    const lines = block.trim().split('\n');
    if (lines.length < 2) continue;
    const tsLine = lines[0].includes('-->') ? lines[0] : (lines[1] || '');
    if (!tsLine.includes('-->')) continue;
    const [rawStart, rawEnd] = tsLine.split('-->').map((x) => x.trim().split(' ')[0]);
    const aSec = parseSubtitleTimestamp(rawStart);
    const bSec = parseSubtitleTimestamp(rawEnd);
    if (!Number.isFinite(aSec) || !Number.isFinite(bSec)) continue;
    if (bSec <= segmentStart || aSec >= segmentEnd) continue;
    const shiftedStart = Math.max(0, aSec - segmentStart);
    const shiftedEnd = Math.min(duration, bSec - segmentStart);
    const textLines = lines.slice(tsLine === lines[0] ? 1 : 2);
    out.push(`${out.length + 1}\n${formatSubtitleTimestamp(shiftedStart)} --> ${formatSubtitleTimestamp(shiftedEnd)}\n${textLines.join('\n')}`);
  }
  return out.length ? out.join('\n\n') + '\n' : null;
}
function findSubtitleFile(files) {
  const preferred = ['source.id.srt', 'source.en.srt', 'source.id.vtt', 'source.en.vtt'];
  for (const name of preferred) if (files.includes(name)) return name;
  return files.find((n) => n.startsWith('source.') && (n.endsWith('.srt') || n.endsWith('.vtt'))) || null;
}
const SUBTITLE_STYLE = "FontSize=26,PrimaryColour=&HFFFFFF,OutlineColour=&H80000000,BorderStyle=3,Outline=2,Shadow=0,MarginV=80";


function parseSubtitleEntries(content) {
  const blocks = content.replace(/\r\n/g, '\n').split(/\n\s*\n/);
  const entries = [];
  for (const block of blocks) {
    const lines = block.trim().split('\n');
    if (lines.length < 2) continue;
    if (lines[0].trim().toUpperCase() === 'WEBVTT') continue;
    const tsLine = lines[0].includes('-->') ? lines[0] : (lines[1] || '');
    if (!tsLine.includes('-->')) continue;
    const [rawStart, rawEnd] = tsLine.split('-->').map((x) => x.trim().split(' ')[0]);
    const aSec = parseSubtitleTimestamp(rawStart);
    const bSec = parseSubtitleTimestamp(rawEnd);
    if (!Number.isFinite(aSec) || !Number.isFinite(bSec) || bSec <= aSec) continue;
    const text = lines.slice(tsLine === lines[0] ? 1 : 2).join(' ').replace(/<[^>]+>/g, '').trim();
    if (text) entries.push({ start: aSec, end: bSec, text });
  }
  return entries;
}
const VIRAL_KEYWORDS = [
  ['kenapa', 5], ['mengapa', 5], ['bagaimana', 4], ['tahukah', 5], ['bayangkan', 4],
  ['ternyata', 5], ['mengejutkan', 5], ['rahasia', 6], ['terbongkar', 5], ['fakta', 4],
  ['jangan', 4], ['gratis', 5], ['terbukti', 4], ['cara', 3], ['tips', 3], ['trik', 4],
  ['tutorial', 3], ['solusi', 3], ['gagal', 3], ['sukses', 3], ['pertama', 3],
  ['terbesar', 3], ['viral', 4], ['wajib', 4], ['bukti', 4], ['hasilnya', 3],
  ['akhirnya', 3], ['wow', 3], ['gila', 3], ['parah', 3], ['seru', 3],
  ['why', 3], ['secret', 5], ['shocking', 4], ['free', 3], ['proven', 3],
  ['how to', 4], ['mistake', 4], ['never', 3], ['amazing', 3],
];
function scoreChunkText(text) {
  const lower = text.toLowerCase();
  let score = 0;
  const matched = [];
  for (const [keyword, weight] of VIRAL_KEYWORDS) {
    if (lower.includes(keyword)) { score += weight; matched.push(keyword); }
  }
  score += (lower.match(/\?/g) || []).length * 3;
  score += (lower.match(/!/g) || []).length * 2;
  if (/\d/.test(lower)) score += 4;
  if (lower.includes('%')) score += 2;
  const wordCount = lower.split(/\s+/).filter(Boolean).length;
  if (wordCount >= 20 && wordCount <= 120) score += 3;
  if (lower.includes('[musik') || lower.includes('[music') || lower.includes('\u266a')) score -= 10;
  return { score, keywords: matched.slice(0, 5) };
}
function chunkSubtitleEntries(entries, clipDuration) {
  const chunks = [];
  let current = [];
  let start = null;
  let prevEnd = null;
  for (const entry of entries) {
    if (!current.length) {
      start = entry.start;
    } else if (entry.start - start >= clipDuration || entry.start - prevEnd > 60) {
      // Jendela waktu habis, atau ada jeda hening >60 detik: tutup chunk lama.
      // Ini mencegah satu chunk "menelan" entri yang jauh di kemudian hari.
      chunks.push({ start: Math.floor(start), text: current.map((e) => e.text).join(' ') });
      current = [];
      start = entry.start;
    }
    current.push(entry);
    prevEnd = entry.end;
  }
  if (current.length && current[current.length - 1].end - start >= 10) {
    chunks.push({ start: Math.floor(start), text: current.map((e) => e.text).join(' ') });
  }
  return chunks;
}
function pickViralChunks(chunks, count) {
  const scored = chunks.map((chunk, index) => ({ ...chunk, index, ...scoreChunkText(chunk.text) }));
  scored.sort((a, b) => b.score - a.score);
  const picked = [];
  for (const candidate of scored) {
    if (picked.some((chosen) => Math.abs(chosen.index - candidate.index) <= 1)) continue;
    picked.push(candidate);
    if (picked.length >= count) break;
  }
  picked.sort((a, b) => a.start - b.start);
  return picked;
}

app.post('/api/clip', async (req, res) => {
  const { videoId, start, end, aspectRatio = '9:16', quality = '720p', subtitles = false, hook = false, hookDuration } = req.body || {};
  let startSeconds = parseTime(start);
  let endSeconds = parseTime(end);
  const hookDurationSeconds = typeof hookDuration === 'number' ? hookDuration : parseTime(String(hookDuration ?? ''));
  if (hook) { startSeconds = 0; endSeconds = Number.isFinite(hookDurationSeconds) ? hookDurationSeconds : 30; }

  try {
    if (!/^[A-Za-z0-9_-]{11}$/.test(String(videoId || ''))) fail('YouTube video ID tidak valid.');
    if (hook && (!Number.isFinite(hookDurationSeconds) || hookDurationSeconds < 5 || hookDurationSeconds > 600)) fail('Durasi hook harus 5 sampai 600 detik.');
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
      const ytdlpArgs = ['--no-playlist', '-f', `bestvideo[height<=${QUALITY[quality]}]+bestaudio/best[height<=${QUALITY[quality]}]`, '--merge-output-format', 'mp4', '-o', source, `https://www.youtube.com/watch?v=${videoId}`];
      if (subtitles) ytdlpArgs.push('--write-subs', '--write-auto-subs', '--sub-lang', 'id,en', '--convert-subs', 'srt');
      await execFileAsync('yt-dlp', ytdlpArgs, { maxBuffer: 10 * 1024 * 1024 });
      const downloaded = (await fs.readdir(jobDir)).find((name) => name.startsWith('source.'));
      if (!downloaded) fail('Video tidak berhasil diunduh.', 502);
      const downloadedPath = path.join(jobDir, downloaded);
      if (downloadedPath !== input) await fs.rename(downloadedPath, input);
      let subtitleContent = null;
      if (subtitles) {
        const subFile = findSubtitleFile(await fs.readdir(jobDir));
        if (subFile) subtitleContent = await fs.readFile(path.join(jobDir, subFile), 'utf8');
      }
      const [rw, rh] = RATIOS[aspectRatio];
      const filter = `scale=if(gt(a\\,${rw}/${rh})\\,ih*${rw}/${rh}\\,iw):if(gt(a\\,${rw}/${rh})\\,ih\\,iw*${rh}/${rw}),crop=iw:ih,setsar=1`;
      let videoFilter = filter;
      let subtitled = false;
      if (subtitleContent) {
        const sliced = sliceSubtitles(subtitleContent, startSeconds, endSeconds - startSeconds);
        if (sliced) { await fs.writeFile(path.join(jobDir, 'segment.srt'), sliced, 'utf8'); videoFilter = `${filter},subtitles='segment.srt':force_style='${SUBTITLE_STYLE}'`; subtitled = true; }
      }
      await execFileAsync('ffmpeg', ['-y', '-ss', String(startSeconds), '-i', input, '-t', String(endSeconds - startSeconds), '-vf', videoFilter, '-c:v', 'libx264', '-preset', 'fast', '-crf', '23', '-c:a', 'aac', '-b:a', '128k', '-movflags', '+faststart', output], { maxBuffer: 10 * 1024 * 1024, cwd: jobDir });
      res.json({ downloadUrl: `/downloads/${outputName}`, subtitled, ...(subtitles && !subtitled ? { subtitleNote: 'Subtitle tidak ditemukan untuk video ini, clip dibuat tanpa subtitle.' } : {}) });
    } finally {
      await fs.rm(jobDir, { recursive: true, force: true });
    }
  } catch (error) {
    const status = error.status || 500;
    console.error(error);
    res.status(status).json({ error: status === 500 ? 'Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah terpasang.' : error.message });
  }
});


app.post('/api/clips', async (req, res) => {
  const { videoId, start, clipDuration, count, gap, aspectRatio = '9:16', quality = '720p', subtitles = false } = req.body || {};
  const startSeconds = parseTime(start);
  const clipSeconds = typeof clipDuration === 'number' ? clipDuration : parseTime(String(clipDuration ?? ''));
  const clipCount = Number(count);
  const gapSeconds = gap === undefined || gap === '' ? 0 : Number(gap);

  try {
    if (!/^[A-Za-z0-9_-]{11}$/.test(String(videoId || ''))) fail('YouTube video ID tidak valid.');
    if (!Number.isFinite(startSeconds)) fail('Format waktu mulai harus MM:SS atau HH:MM:SS.');
    if (!Number.isFinite(clipSeconds) || clipSeconds < 5 || clipSeconds > 600) fail('Durasi per clip harus 5 sampai 600 detik.');
    if (!Number.isInteger(clipCount) || clipCount < 1 || clipCount > 20) fail('Jumlah clip harus 1 sampai 20.');
    if (!Number.isFinite(gapSeconds) || gapSeconds < 0 || gapSeconds > 3600) fail('Jeda antar clip harus 0 sampai 3600 detik.');
    if (!RATIOS[aspectRatio]) fail('Aspect ratio tidak didukung.');
    if (!QUALITY[quality]) fail('Kualitas tidak didukung.');

    await Promise.all([fs.mkdir(DOWNLOADS_DIR, { recursive: true }), fs.mkdir(TEMP_DIR, { recursive: true })]);
    const jobId = crypto.randomUUID();
    const jobDir = path.join(TEMP_DIR, jobId);
    const source = path.join(jobDir, 'source.%(ext)s');
    const input = path.join(jobDir, 'source.mp4');
    await fs.mkdir(jobDir, { recursive: true });

    try {
      // Unduh SEKALI saja, lalu potong berkali-kali dari file yang sama.
      // Jauh lebih hemat daripada mengunduh ulang video 1 jam untuk tiap clip.
      const ytdlpArgs = ['--no-playlist', '-f', `bestvideo[height<=${QUALITY[quality]}]+bestaudio/best[height<=${QUALITY[quality]}]`, '--merge-output-format', 'mp4', '-o', source, `https://www.youtube.com/watch?v=${videoId}`];
      if (subtitles) ytdlpArgs.push('--write-subs', '--write-auto-subs', '--sub-lang', 'id,en', '--convert-subs', 'srt');
      await execFileAsync('yt-dlp', ytdlpArgs, { maxBuffer: 10 * 1024 * 1024 });
      const downloaded = (await fs.readdir(jobDir)).find((name) => name.startsWith('source.'));
      if (!downloaded) fail('Video tidak berhasil diunduh.', 502);
      const downloadedPath = path.join(jobDir, downloaded);
      if (downloadedPath !== input) await fs.rename(downloadedPath, input);
      let subtitleContent = null;
      if (subtitles) {
        const subFile = findSubtitleFile(await fs.readdir(jobDir));
        if (subFile) subtitleContent = await fs.readFile(path.join(jobDir, subFile), 'utf8');
      }
      const [rw, rh] = RATIOS[aspectRatio];
      const filter = `scale=if(gt(a\\,${rw}/${rh})\\,ih*${rw}/${rh}\\,iw):if(gt(a\\,${rw}/${rh})\\,ih\\,iw*${rh}/${rw}),crop=iw:ih,setsar=1`;
      const clips = [];
      for (let i = 0; i < clipCount; i++) {
        const segmentStart = startSeconds + i * (clipSeconds + gapSeconds);
        const segmentEnd = segmentStart + clipSeconds;
        const outputName = `${jobId}-${i + 1}.mp4`;
        const output = path.join(DOWNLOADS_DIR, outputName);
        let videoFilter = filter;
        let subtitled = false;
        if (subtitleContent) {
          const sliced = sliceSubtitles(subtitleContent, segmentStart, clipSeconds);
          if (sliced) { await fs.writeFile(path.join(jobDir, `segment-${i + 1}.srt`), sliced, 'utf8'); videoFilter = `${filter},subtitles='segment-${i + 1}.srt':force_style='${SUBTITLE_STYLE}'`; subtitled = true; }
        }
        await execFileAsync('ffmpeg', ['-y', '-ss', String(segmentStart), '-i', input, '-t', String(clipSeconds), '-vf', videoFilter, '-c:v', 'libx264', '-preset', 'fast', '-crf', '23', '-c:a', 'aac', '-b:a', '128k', '-movflags', '+faststart', output], { maxBuffer: 10 * 1024 * 1024, cwd: jobDir });
        clips.push({ index: i + 1, start: formatTime(segmentStart), end: formatTime(segmentEnd), downloadUrl: `/downloads/${outputName}`, subtitled });
      }
      res.json({ clips });
    } finally {
      await fs.rm(jobDir, { recursive: true, force: true });
    }
  } catch (error) {
    const status = error.status || 500;
    console.error(error);
    res.status(status).json({ error: status === 500 ? 'Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah terpasang.' : error.message });
  }
});


app.post('/api/viral', async (req, res) => {
  const { videoId, clipDuration, count, aspectRatio = '9:16', quality = '720p', subtitles = false } = req.body || {};
  const clipSeconds = typeof clipDuration === 'number' ? clipDuration : parseTime(String(clipDuration ?? ''));
  const clipCount = Number(count);

  try {
    if (!/^[A-Za-z0-9_-]{11}$/.test(String(videoId || ''))) fail('YouTube video ID tidak valid.');
    if (!Number.isFinite(clipSeconds) || clipSeconds < 5 || clipSeconds > 600) fail('Durasi per clip harus 5 sampai 600 detik.');
    if (!Number.isInteger(clipCount) || clipCount < 1 || clipCount > 20) fail('Jumlah clip harus 1 sampai 20.');
    if (!RATIOS[aspectRatio]) fail('Aspect ratio tidak didukung.');
    if (!QUALITY[quality]) fail('Kualitas tidak didukung.');

    await Promise.all([fs.mkdir(DOWNLOADS_DIR, { recursive: true }), fs.mkdir(TEMP_DIR, { recursive: true })]);
    const jobId = crypto.randomUUID();
    const jobDir = path.join(TEMP_DIR, jobId);
    const source = path.join(jobDir, 'source.%(ext)s');
    const input = path.join(jobDir, 'source.mp4');
    await fs.mkdir(jobDir, { recursive: true });

    try {
      // Langkah 1: ambil caption saja dulu (tanpa mengunduh video 1 jam),
      // lalu analisis transkripnya untuk mencari momen berpotensi viral.
      const subArgs = ['--skip-download', '--write-subs', '--write-auto-subs', '--sub-lang', 'id,en', '--convert-subs', 'srt', '-o', source, `https://www.youtube.com/watch?v=${videoId}`];
      await execFileAsync('yt-dlp', subArgs, { maxBuffer: 10 * 1024 * 1024 });
      const subFile = findSubtitleFile(await fs.readdir(jobDir));
      if (!subFile) fail('Video ini tidak memiliki caption, jadi momen viral tidak bisa dianalisis dari transkrip. Gunakan mode Beberapa clip otomatis.', 400);
      const subtitleContent = await fs.readFile(path.join(jobDir, subFile), 'utf8');
      const entries = parseSubtitleEntries(subtitleContent);
      if (entries.length < 5) fail('Caption video ini terlalu pendek untuk dianalisis. Gunakan mode Beberapa clip otomatis.', 400);
      const chunks = chunkSubtitleEntries(entries, clipSeconds);
      const picked = pickViralChunks(chunks, clipCount);
      if (!picked.length) fail('Tidak ditemukan momen yang menonjol dari transkrip video ini.', 400);

      // Langkah 2: unduh video sekali, lalu potong hanya momen terpilih.
      await execFileAsync('yt-dlp', ['--no-playlist', '-f', `bestvideo[height<=${QUALITY[quality]}]+bestaudio/best[height<=${QUALITY[quality]}]`, '--merge-output-format', 'mp4', '-o', source, `https://www.youtube.com/watch?v=${videoId}`], { maxBuffer: 10 * 1024 * 1024 });
      const downloaded = (await fs.readdir(jobDir)).find((name) => name.startsWith('source.'));
      if (!downloaded) fail('Video tidak berhasil diunduh.', 502);
      const downloadedPath = path.join(jobDir, downloaded);
      if (downloadedPath !== input) await fs.rename(downloadedPath, input);
      const [rw, rh] = RATIOS[aspectRatio];
      const filter = `scale=if(gt(a\\,${rw}/${rh})\\,ih*${rw}/${rh}\\,iw):if(gt(a\\,${rw}/${rh})\\,ih\\,iw*${rh}/${rw}),crop=iw:ih,setsar=1`;
      const clips = [];
      for (let i = 0; i < picked.length; i++) {
        const segmentStart = picked[i].start;
        const segmentEnd = segmentStart + clipSeconds;
        const outputName = `${jobId}-viral-${i + 1}.mp4`;
        const output = path.join(DOWNLOADS_DIR, outputName);
        let videoFilter = filter;
        let subtitled = false;
        if (subtitles) {
          const sliced = sliceSubtitles(subtitleContent, segmentStart, clipSeconds);
          if (sliced) { await fs.writeFile(path.join(jobDir, `viral-${i + 1}.srt`), sliced, 'utf8'); videoFilter = `${filter},subtitles='viral-${i + 1}.srt':force_style='${SUBTITLE_STYLE}'`; subtitled = true; }
        }
        await execFileAsync('ffmpeg', ['-y', '-ss', String(segmentStart), '-i', input, '-t', String(clipSeconds), '-vf', videoFilter, '-c:v', 'libx264', '-preset', 'fast', '-crf', '23', '-c:a', 'aac', '-b:a', '128k', '-movflags', '+faststart', output], { maxBuffer: 10 * 1024 * 1024, cwd: jobDir });
        clips.push({ index: i + 1, start: formatTime(segmentStart), end: formatTime(segmentEnd), downloadUrl: `/downloads/${outputName}`, score: picked[i].score, keywords: picked[i].keywords, subtitled });
      }
      res.json({ clips, note: 'Skor dihitung dari kata kunci dan pola kalimat di transkrip (heuristik), bukan jaminan viral.' });
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
