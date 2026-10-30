package main

import (
	"strings"
	"testing"
)

func TestParseTime(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"30", 30, true},
		{"00:30", 30, true},
		{"01:02:03", 3723, true},
		{"", 0, false},
		{"abc", 0, false},
		{"00:99", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTime(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseTime(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFormatTime(t *testing.T) {
	if got := formatTime(3723); got != "1:02:03" {
		t.Errorf("formatTime(3723) = %q, want 1:02:03", got)
	}
	if got := formatTime(90); got != "01:30" {
		t.Errorf("formatTime(90) = %q, want 01:30", got)
	}
}

func TestSliceSubtitles(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:04,000\nHalo\n\n2\n00:00:05,500 --> 00:00:08,000\nTengah\n\n3\n00:01:10,000 --> 00:01:13,000\nNanti\n"
	if got := sliceSubtitles(srt, 0, 10); !strings.Contains(got, "Halo") || !strings.Contains(got, "Tengah") || strings.Contains(got, "Nanti") {
		t.Errorf("slice 0-10 salah:\n%s", got)
	}
	if got := sliceSubtitles(srt, 60, 15); !strings.Contains(got, "00:00:10,000 --> 00:00:13,000") {
		t.Errorf("slice 60-75 seharusnya digeser ke 00:00:10:\n%s", got)
	}
	if got := sliceSubtitles(srt, 20, 10); got != "" {
		t.Errorf("slice tanpa tumpang tindih seharusnya kosong, dapat:\n%s", got)
	}
}

func TestPickViralChunks(t *testing.T) {
	srt := "1\n00:00:01,000 --> 00:00:10,000\nHari ini saya makan nasi goreng biasa saja\n\n" +
		"2\n00:10:00,000 --> 00:10:12,000\nRahasia terbesar yang tidak pernah dibongkar, ternyata begini!\n\n" +
		"3\n00:20:00,000 --> 00:20:12,000\nCuaca hari ini cukup baik untuk berjalan kaki\n\n" +
		"4\n00:30:00,000 --> 00:30:12,000\nKenapa 99% orang gagal? Ini jawabannya, gratis dan terbukti!\n"
	entries := parseSubtitleEntries(srt)
	chunks := chunkSubtitleEntries(entries, 30)
	picked := pickViralChunks(chunks, 2)
	if len(picked) != 2 {
		t.Fatalf("picked = %d, want 2", len(picked))
	}
	if picked[0].Start != 600 || picked[1].Start != 1800 {
		t.Errorf("picked starts = %v, %v; want 600, 1800", picked[0].Start, picked[1].Start)
	}
}

func TestBuildFilter(t *testing.T) {
	want := `scale=if(gt(a\,9/16)\,ih*9/16\,iw):if(gt(a\,9/16)\,ih\,iw*16/9),crop=iw:ih,setsar=1`
	if got := buildFilter(ratio{9, 16}); got != want {
		t.Errorf("buildFilter(9,16) = %q\nwant %q", got, want)
	}
}
