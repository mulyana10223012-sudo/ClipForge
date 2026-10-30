// ClipForge — backend Go (pengganti server.js Node.js).
// Fungsinya sama persis: melayani antarmuka web di public/,
// endpoint /api/clip, /api/clips, dan /api/viral, dengan memanggil
// yt-dlp untuk mengunduh dan FFmpeg untuk memotong video.
// Satu-satunya dependensi eksternal tetap FFmpeg dan yt-dlp
// (harus ada di PATH); tidak ada dependensi Go dari luar,
// cukup pustaka standar.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var qualityMap = map[string]string{"1080p": "1080", "720p": "720", "480p": "480"}

type ratio struct{ w, h int }

var ratioMap = map[string]ratio{"9:16": {9, 16}, "16:9": {16, 9}, "1:1": {1, 1}}

const subtitleStyle = "FontSize=26,PrimaryColour=&HFFFFFF,OutlineColour=&H80000000,BorderStyle=3,Outline=2,Shadow=0,MarginV=80"

const genericError = "Gagal memproses video. Pastikan FFmpeg dan yt-dlp sudah terpasang."

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return e.Message }

func fail(message string, status int) *apiError { return &apiError{Status: status, Message: message} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*apiError); ok {
		writeJSON(w, apiErr.Status, map[string]string{"error": apiErr.Message})
		return
	}
	log.Printf("error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": genericError})
}

// ---------- waktu ----------

var timeRe = regexp.MustCompile(`^\d{1,3}(?::\d{1,2}){0,2}$`)

// parseTime menerima "SS", "MM:SS", atau "HH:MM:SS" seperti versi Node.
// Mengembalikan detik dan false bila format tidak valid.
func parseTime(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if !timeRe.MatchString(value) {
		return 0, false
	}
	parts := strings.Split(value, ":")
	nums := make([]float64, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 59 {
			return 0, false
		}
		nums[i] = float64(n)
	}
	switch len(nums) {
	case 1:
		return nums[0], true
	case 2:
		return nums[0]*60 + nums[1], true
	default:
		return nums[0]*3600 + nums[1]*60 + nums[2], true
	}
}

func formatTime(totalSeconds float64) string {
	total := int(math.Floor(totalSeconds))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// durationValue menerima angka JSON (detik) atau string waktu ("MM:SS").
func durationValue(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return num, true
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return parseTime(str)
	}
	return 0, false
}

func newJobID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := buf.String()
		if len(out) > 500 {
			out = out[:500]
		}
		return fmt.Errorf("%s %v: %w (output: %s)", name, args, err, out)
	}
	return nil
}

// ---------- subtitle ----------

func parseSubtitleTimestamp(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	parts := strings.SplitN(text, ".", 2)
	if len(parts) == 1 {
		parts = strings.SplitN(text, ",", 2)
	}
	if len(parts) != 2 {
		return 0, false
	}
	timeParts := strings.Split(parts[0], ":")
	frac, err := strconv.ParseFloat("0."+parts[1], 64)
	if err != nil {
		return 0, false
	}
	var seconds float64
	switch len(timeParts) {
	case 3:
		h, e1 := strconv.Atoi(timeParts[0])
		m, e2 := strconv.Atoi(timeParts[1])
		s, e3 := strconv.Atoi(timeParts[2])
		if e1 != nil || e2 != nil || e3 != nil {
			return 0, false
		}
		seconds = float64(h*3600 + m*60 + s)
	case 2:
		m, e1 := strconv.Atoi(timeParts[0])
		s, e2 := strconv.Atoi(timeParts[1])
		if e1 != nil || e2 != nil {
			return 0, false
		}
		seconds = float64(m*60 + s)
	default:
		return 0, false
	}
	return seconds + frac, true
}

func formatSubtitleTimestamp(totalSeconds float64) string {
	ms := int64(math.Max(0, math.Round(totalSeconds*1000)))
	h := ms / 3600000
	mi := (ms % 3600000) / 60000
	s := (ms % 60000) / 1000
	m := ms % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, mi, s, m)
}

// sliceSubtitles memotong + menggeser subtitle ke rentang clip.
// Mengembalikan string kosong bila tidak ada entri yang tumpang tindih.
func sliceSubtitles(content string, segmentStart, duration float64) string {
	segmentEnd := segmentStart + duration
	blocks := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	var out []string
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 2 {
			continue
		}
		tsLine := lines[0]
		textStart := 1
		if !strings.Contains(tsLine, "-->") {
			if len(lines) < 2 {
				continue
			}
			tsLine = lines[1]
			textStart = 2
		}
		if !strings.Contains(tsLine, "-->") {
			continue
		}
		parts := strings.Split(tsLine, "-->")
		if len(parts) != 2 {
			continue
		}
		rawStart := strings.Fields(strings.TrimSpace(parts[0]))[0]
		rawEnd := strings.Fields(strings.TrimSpace(parts[1]))[0]
		aSec, ok1 := parseSubtitleTimestamp(rawStart)
		bSec, ok2 := parseSubtitleTimestamp(rawEnd)
		if !ok1 || !ok2 {
			continue
		}
		if bSec <= segmentStart || aSec >= segmentEnd {
			continue
		}
		shiftedStart := math.Max(0, aSec-segmentStart)
		shiftedEnd := math.Min(duration, bSec-segmentStart)
		text := strings.Join(lines[textStart:], "\n")
		out = append(out, fmt.Sprintf("%d\n%s --> %s\n%s", len(out)+1, formatSubtitleTimestamp(shiftedStart), formatSubtitleTimestamp(shiftedEnd), text))
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n") + "\n"
}

func findSubtitleFile(files []string) string {
	preferred := []string{"source.id.srt", "source.en.srt", "source.id.vtt", "source.en.vtt"}
	for _, name := range preferred {
		for _, f := range files {
			if f == name {
				return name
			}
		}
	}
	for _, f := range files {
		if strings.HasPrefix(f, "source.") && (strings.HasSuffix(f, ".srt") || strings.HasSuffix(f, ".vtt")) {
			return f
		}
	}
	return ""
}

// ---------- analisis momen viral ----------

type subtitleEntry struct {
	Start float64
	End   float64
	Text  string
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func parseSubtitleEntries(content string) []subtitleEntry {
	blocks := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	var entries []subtitleEntry
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 2 {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(lines[0])) == "WEBVTT" {
			continue
		}
		tsLine := lines[0]
		textStart := 1
		if !strings.Contains(tsLine, "-->") {
			if len(lines) < 2 {
				continue
			}
			tsLine = lines[1]
			textStart = 2
		}
		if !strings.Contains(tsLine, "-->") {
			continue
		}
		parts := strings.Split(tsLine, "-->")
		if len(parts) != 2 {
			continue
		}
		rawStart := strings.Fields(strings.TrimSpace(parts[0]))[0]
		rawEnd := strings.Fields(strings.TrimSpace(parts[1]))[0]
		aSec, ok1 := parseSubtitleTimestamp(rawStart)
		bSec, ok2 := parseSubtitleTimestamp(rawEnd)
		if !ok1 || !ok2 || bSec <= aSec {
			continue
		}
		text := strings.TrimSpace(tagRe.ReplaceAllString(strings.Join(lines[textStart:], " "), ""))
		if text != "" {
			entries = append(entries, subtitleEntry{Start: aSec, End: bSec, Text: text})
		}
	}
	return entries
}

type keywordWeight struct {
	Word   string
	Weight int
}

var viralKeywords = []keywordWeight{
	{"kenapa", 5}, {"mengapa", 5}, {"bagaimana", 4}, {"tahukah", 5}, {"bayangkan", 4},
	{"ternyata", 5}, {"mengejutkan", 5}, {"rahasia", 6}, {"terbongkar", 5}, {"fakta", 4},
	{"jangan", 4}, {"gratis", 5}, {"terbukti", 4}, {"cara", 3}, {"tips", 3}, {"trik", 4},
	{"tutorial", 3}, {"solusi", 3}, {"gagal", 3}, {"sukses", 3}, {"pertama", 3},
	{"terbesar", 3}, {"viral", 4}, {"wajib", 4}, {"bukti", 4}, {"hasilnya", 3},
	{"akhirnya", 3}, {"wow", 3}, {"gila", 3}, {"parah", 3}, {"seru", 3},
	{"why", 3}, {"secret", 5}, {"shocking", 4}, {"free", 3}, {"proven", 3},
	{"how to", 4}, {"mistake", 4}, {"never", 3}, {"amazing", 3},
}

func scoreChunkText(text string) (int, []string) {
	lower := strings.ToLower(text)
	score := 0
	var matched []string
	for _, kw := range viralKeywords {
		if strings.Contains(lower, kw.Word) {
			score += kw.Weight
			matched = append(matched, kw.Word)
		}
	}
	score += strings.Count(lower, "?") * 3
	score += strings.Count(lower, "!") * 2
	if strings.ContainsAny(lower, "0123456789") {
		score += 4
	}
	if strings.Contains(lower, "%") {
		score += 2
	}
	words := len(strings.Fields(lower))
	if words >= 20 && words <= 120 {
		score += 3
	}
	if strings.Contains(lower, "[musik") || strings.Contains(lower, "[music") || strings.Contains(lower, "♪") {
		score -= 10
	}
	if len(matched) > 5 {
		matched = matched[:5]
	}
	return score, matched
}

type viralChunk struct {
	Start    float64
	Text     string
	Index    int
	Score    int
	Keywords []string
}

func chunkSubtitleEntries(entries []subtitleEntry, clipDuration float64) []viralChunk {
	var chunks []viralChunk
	var current []subtitleEntry
	var start, prevEnd float64
	flush := func() {
		if len(current) == 0 {
			return
		}
		var texts []string
		for _, e := range current {
			texts = append(texts, e.Text)
		}
		chunks = append(chunks, viralChunk{Start: math.Floor(start), Text: strings.Join(texts, " ")})
	}
	for _, entry := range entries {
		if len(current) == 0 {
			start = entry.Start
		} else if entry.Start-start >= clipDuration || entry.Start-prevEnd > 60 {
			flush()
			current = nil
			start = entry.Start
		}
		current = append(current, entry)
		prevEnd = entry.End
	}
	if len(current) > 0 && current[len(current)-1].End-start >= 10 {
		flush()
	}
	return chunks
}

func pickViralChunks(chunks []viralChunk, count int) []viralChunk {
	scored := make([]viralChunk, len(chunks))
	copy(scored, chunks)
	for i := range scored {
		scored[i].Index = i
		scored[i].Score, scored[i].Keywords = scoreChunkText(scored[i].Text)
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	var picked []viralChunk
	for _, cand := range scored {
		adjacent := false
		for _, chosen := range picked {
			if math.Abs(float64(chosen.Index-cand.Index)) <= 1 {
				adjacent = true
				break
			}
		}
		if adjacent {
			continue
		}
		picked = append(picked, cand)
		if len(picked) >= count {
			break
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].Start < picked[j].Start })
	return picked
}

// buildFilter menghasilkan string filter FFmpeg yang sama dengan versi Node:
// scale + crop sesuai aspect ratio.
func buildFilter(r ratio) string {
	return fmt.Sprintf("scale=if(gt(a\\,%d/%d)\\,ih*%d/%d\\,iw):if(gt(a\\,%d/%d)\\,ih\\,iw*%d/%d),crop=iw:ih,setsar=1",
		r.w, r.h, r.w, r.h, r.w, r.h, r.h, r.w)
}

// ---------- unduh & potong ----------

func validVideoID(id string) bool {
	if len(id) != 11 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func ytdlpFormat(quality string) string {
	h := qualityMap[quality]
	return fmt.Sprintf("bestvideo[height<=%s]+bestaudio/best[height<=%s]", h, h)
}

// downloadVideo mengunduh video sekali. Berbeda dengan versi Node yang
// memakai entri pertama berawalan "source.", di sini berkas subtitle
// (.srt/.vtt) sengaja dilewati supaya tidak tertukar dengan video.
func downloadVideo(videoID, quality, jobDir, source string, withSubs bool) (string, error) {
	args := []string{"--no-playlist", "-f", ytdlpFormat(quality), "--merge-output-format", "mp4", "-o", source,
		"https://www.youtube.com/watch?v=" + videoID}
	if withSubs {
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-lang", "id,en", "--convert-subs", "srt")
	}
	if err := runCmd("", "yt-dlp", args...); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "source.") && !strings.HasSuffix(name, ".srt") && !strings.HasSuffix(name, ".vtt") {
			input := filepath.Join(jobDir, "source.mp4")
			if filepath.Join(jobDir, name) != input {
				if err := os.Rename(filepath.Join(jobDir, name), input); err != nil {
					return "", err
				}
			}
			return input, nil
		}
	}
	return "", fail("Video tidak berhasil diunduh.", 502)
}

func readSubtitleContent(jobDir string) string {
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	name := findSubtitleFile(names)
	if name == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(jobDir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func renderClip(input, output string, start, duration float64, filter, subtitleFile, jobDir string) error {
	videoFilter := filter
	if subtitleFile != "" {
		videoFilter = fmt.Sprintf("%s,subtitles='%s':force_style='%s'", filter, subtitleFile, subtitleStyle)
	}
	args := []string{"-y", "-ss", strconv.FormatFloat(start, 'f', -1, 64), "-i", input,
		"-t", strconv.FormatFloat(duration, 'f', -1, 64), "-vf", videoFilter,
		"-c:v", "libx264", "-preset", "fast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", output}
	return runCmd(jobDir, "ffmpeg", args...)
}

// subtitleForSegment memotong subtitle ke rentang clip dan menulisnya
// ke jobDir; mengembalikan nama berkas relatif (untuk filter FFmpeg)
// dan apakah subtitle berhasil disiapkan.
func subtitleForSegment(content, filename string, start, duration float64, jobDir string) (string, bool) {
	if content == "" {
		return "", false
	}
	sliced := sliceSubtitles(content, start, duration)
	if sliced == "" {
		return "", false
	}
	if err := os.WriteFile(filepath.Join(jobDir, filename), []byte(sliced), 0644); err != nil {
		return "", false
	}
	return filename, true
}

// ---------- request / response ----------

type clipRequest struct {
	VideoID      string          `json:"videoId"`
	Start        string          `json:"start"`
	End          string          `json:"end"`
	AspectRatio  string          `json:"aspectRatio"`
	Quality      string          `json:"quality"`
	Subtitles    bool            `json:"subtitles"`
	Hook         bool            `json:"hook"`
	HookDuration json.RawMessage `json:"hookDuration"`
}

type clipsRequest struct {
	VideoID      string          `json:"videoId"`
	Start        string          `json:"start"`
	ClipDuration json.RawMessage `json:"clipDuration"`
	Count        int             `json:"count"`
	Gap          float64         `json:"gap"`
	AspectRatio  string          `json:"aspectRatio"`
	Quality      string          `json:"quality"`
	Subtitles    bool            `json:"subtitles"`
}

type viralRequest struct {
	VideoID      string          `json:"videoId"`
	ClipDuration json.RawMessage `json:"clipDuration"`
	Count        int             `json:"count"`
	AspectRatio  string          `json:"aspectRatio"`
	Quality      string          `json:"quality"`
	Subtitles    bool            `json:"subtitles"`
}

type clipInfo struct {
	Index       int      `json:"index,omitempty"`
	Start       string   `json:"start,omitempty"`
	End         string   `json:"end,omitempty"`
	DownloadURL string   `json:"downloadUrl"`
	Score       int      `json:"score,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Subtitled   bool     `json:"subtitled"`
}

type clipResponse struct {
	DownloadURL  string `json:"downloadUrl"`
	Subtitled    bool   `json:"subtitled"`
	SubtitleNote string `json:"subtitleNote,omitempty"`
}

func defaults(aspect, quality string) (ratio, string, error) {
	if aspect == "" {
		aspect = "9:16"
	}
	if quality == "" {
		quality = "720p"
	}
	r, ok := ratioMap[aspect]
	if !ok {
		return ratio{}, "", fail("Aspect ratio tidak didukung.", 400)
	}
	if _, ok := qualityMap[quality]; !ok {
		return ratio{}, "", fail("Kualitas tidak didukung.", 400)
	}
	return r, quality, nil
}

func setupJob() (jobDir, source string, err error) {
	if err := os.MkdirAll("downloads", 0755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll("temp", 0755); err != nil {
		return "", "", err
	}
	jobID := newJobID()
	jobDir = filepath.Join("temp", jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return "", "", err
	}
	return jobDir, filepath.Join(jobDir, "source.%(ext)s"), nil
}

func handleClip(w http.ResponseWriter, r *http.Request) {
	var req clipRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, fail("Badan permintaan tidak valid.", 400))
		return
	}
	startSeconds, startOK := parseTime(req.Start)
	endSeconds, endOK := parseTime(req.End)
	hookDurationSeconds, _ := durationValue(req.HookDuration)
	if req.Hook {
		startSeconds, startOK = 0, true
		if hookDurationSeconds > 0 {
			endSeconds, endOK = hookDurationSeconds, true
		} else {
			endSeconds, endOK = 30, true
		}
	}
	err := func() error {
		if !validVideoID(req.VideoID) {
			return fail("YouTube video ID tidak valid.", 400)
		}
		if req.Hook && (hookDurationSeconds < 5 || hookDurationSeconds > 600) {
			// Bila hookDuration tidak dikirim, durationValue mengembalikan 0
			// dan kita pakai bawaan 30 detik di atas; jadi hanya tolak bila
			// nilainya ada tapi di luar rentang.
			if len(req.HookDuration) > 0 {
				return fail("Durasi hook harus 5 sampai 600 detik.", 400)
			}
		}
		if !startOK || !endOK {
			return fail("Format waktu harus MM:SS atau HH:MM:SS.", 400)
		}
		if endSeconds <= startSeconds {
			return fail("Waktu akhir harus lebih besar daripada waktu mulai.", 400)
		}
		if endSeconds-startSeconds > 600 {
			return fail("Durasi clip maksimal adalah 10 menit.", 400)
		}
		ratioVal, quality, err := defaults(req.AspectRatio, req.Quality)
		if err != nil {
			return err
		}
		jobDir, source, err := setupJob()
		if err != nil {
			return err
		}
		defer os.RemoveAll(jobDir)
		input, err := downloadVideo(req.VideoID, quality, jobDir, source, req.Subtitles)
		if err != nil {
			return err
		}
		subtitleContent := ""
		if req.Subtitles {
			subtitleContent = readSubtitleContent(jobDir)
		}
		filter := buildFilter(ratioVal)
		duration := endSeconds - startSeconds
		subFile, subtitled := subtitleForSegment(subtitleContent, "segment.srt", startSeconds, duration, jobDir)
		outputName := newJobID() + ".mp4"
		output := filepath.Join("downloads", outputName)
		if err := renderClip(input, output, startSeconds, duration, filter, subFile, jobDir); err != nil {
			return err
		}
		resp := clipResponse{DownloadURL: "/downloads/" + outputName, Subtitled: subtitled}
		if req.Subtitles && !subtitled {
			resp.SubtitleNote = "Subtitle tidak ditemukan untuk video ini, clip dibuat tanpa subtitle."
		}
		writeJSON(w, http.StatusOK, resp)
		return nil
	}()
	if err != nil {
		writeAPIError(w, err)
	}
}

func handleClips(w http.ResponseWriter, r *http.Request) {
	var req clipsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, fail("Badan permintaan tidak valid.", 400))
		return
	}
	startSeconds, startOK := parseTime(req.Start)
	clipSeconds, clipOK := durationValue(req.ClipDuration)
	err := func() error {
		if !validVideoID(req.VideoID) {
			return fail("YouTube video ID tidak valid.", 400)
		}
		if !startOK {
			return fail("Format waktu mulai harus MM:SS atau HH:MM:SS.", 400)
		}
		if !clipOK || clipSeconds < 5 || clipSeconds > 600 {
			return fail("Durasi per clip harus 5 sampai 600 detik.", 400)
		}
		if req.Count < 1 || req.Count > 20 {
			return fail("Jumlah clip harus 1 sampai 20.", 400)
		}
		if req.Gap < 0 || req.Gap > 3600 {
			return fail("Jeda antar clip harus 0 sampai 3600 detik.", 400)
		}
		ratioVal, quality, err := defaults(req.AspectRatio, req.Quality)
		if err != nil {
			return err
		}
		jobDir, source, err := setupJob()
		if err != nil {
			return err
		}
		defer os.RemoveAll(jobDir)
		input, err := downloadVideo(req.VideoID, quality, jobDir, source, req.Subtitles)
		if err != nil {
			return err
		}
		subtitleContent := ""
		if req.Subtitles {
			subtitleContent = readSubtitleContent(jobDir)
		}
		filter := buildFilter(ratioVal)
		var clips []clipInfo
		for i := 0; i < req.Count; i++ {
			segmentStart := startSeconds + float64(i)*(clipSeconds+req.Gap)
			segmentEnd := segmentStart + clipSeconds
			outputName := fmt.Sprintf("%s-%d.mp4", newJobID(), i+1)
			output := filepath.Join("downloads", outputName)
			subFile, subtitled := subtitleForSegment(subtitleContent, fmt.Sprintf("segment-%d.srt", i+1), segmentStart, clipSeconds, jobDir)
			if err := renderClip(input, output, segmentStart, clipSeconds, filter, subFile, jobDir); err != nil {
				return err
			}
			clips = append(clips, clipInfo{Index: i + 1, Start: formatTime(segmentStart), End: formatTime(segmentEnd), DownloadURL: "/downloads/" + outputName, Subtitled: subtitled})
		}
		writeJSON(w, http.StatusOK, map[string]any{"clips": clips})
		return nil
	}()
	if err != nil {
		writeAPIError(w, err)
	}
}

func handleViral(w http.ResponseWriter, r *http.Request) {
	var req viralRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, fail("Badan permintaan tidak valid.", 400))
		return
	}
	clipSeconds, clipOK := durationValue(req.ClipDuration)
	err := func() error {
		if !validVideoID(req.VideoID) {
			return fail("YouTube video ID tidak valid.", 400)
		}
		if !clipOK || clipSeconds < 5 || clipSeconds > 600 {
			return fail("Durasi per clip harus 5 sampai 600 detik.", 400)
		}
		if req.Count < 1 || req.Count > 20 {
			return fail("Jumlah clip harus 1 sampai 20.", 400)
		}
		ratioVal, quality, err := defaults(req.AspectRatio, req.Quality)
		if err != nil {
			return err
		}
		jobDir, source, err := setupJob()
		if err != nil {
			return err
		}
		defer os.RemoveAll(jobDir)
		// Langkah 1: ambil caption saja, analisis transkrip.
		subArgs := []string{"--skip-download", "--write-subs", "--write-auto-subs", "--sub-lang", "id,en", "--convert-subs", "srt", "-o", source,
			"https://www.youtube.com/watch?v=" + req.VideoID}
		if err := runCmd("", "yt-dlp", subArgs...); err != nil {
			return err
		}
		subtitleContent := readSubtitleContent(jobDir)
		if subtitleContent == "" {
			return fail("Video ini tidak memiliki caption, jadi momen viral tidak bisa dianalisis dari transkrip. Gunakan mode Beberapa clip otomatis.", 400)
		}
		entries := parseSubtitleEntries(subtitleContent)
		if len(entries) < 5 {
			return fail("Caption video ini terlalu pendek untuk dianalisis. Gunakan mode Beberapa clip otomatis.", 400)
		}
		picked := pickViralChunks(chunkSubtitleEntries(entries, clipSeconds), req.Count)
		if len(picked) == 0 {
			return fail("Tidak ditemukan momen yang menonjol dari transkrip video ini.", 400)
		}
		// Langkah 2: unduh video sekali, potong hanya momen terpilih.
		input, err := downloadVideo(req.VideoID, quality, jobDir, source, false)
		if err != nil {
			return err
		}
		filter := buildFilter(ratioVal)
		var clips []clipInfo
		for i, chunk := range picked {
			segmentStart := chunk.Start
			segmentEnd := segmentStart + clipSeconds
			outputName := fmt.Sprintf("%s-viral-%d.mp4", newJobID(), i+1)
			output := filepath.Join("downloads", outputName)
			var subFile string
			var subtitled bool
			if req.Subtitles {
				subFile, subtitled = subtitleForSegment(subtitleContent, fmt.Sprintf("viral-%d.srt", i+1), segmentStart, clipSeconds, jobDir)
			}
			if err := renderClip(input, output, segmentStart, clipSeconds, filter, subFile, jobDir); err != nil {
				return err
			}
			clips = append(clips, clipInfo{Index: i + 1, Start: formatTime(segmentStart), End: formatTime(segmentEnd), DownloadURL: "/downloads/" + outputName, Score: chunk.Score, Keywords: chunk.Keywords, Subtitled: subtitled})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"clips": clips,
			"note":  "Skor dihitung dari kata kunci dan pola kalimat di transkrip (heuristik), bukan jaminan viral.",
		})
		return nil
	}()
	if err != nil {
		writeAPIError(w, err)
	}
}

func postOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Metode tidak diizinkan."})
			return
		}
		next(w, r)
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	if err := os.MkdirAll("downloads", 0755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll("temp", 0755); err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/downloads/", http.StripPrefix("/downloads/", http.FileServer(http.Dir("downloads"))))
	mux.HandleFunc("/api/clip", postOnly(handleClip))
	mux.HandleFunc("/api/clips", postOnly(handleClips))
	mux.HandleFunc("/api/viral", postOnly(handleViral))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Metode tidak diizinkan."})
			return
		}
		// Seperti versi Node: semua rute GET mengembalikan halaman utama.
		http.ServeFile(w, r, filepath.Join("public", "index.html"))
	})
	log.Printf("ClipForge berjalan di http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
