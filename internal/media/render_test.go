package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompositionID(t *testing.T) {
	tests := []struct {
		format, orient, want string
		err                  bool
	}{
		{"explained_60s", "16:9", "explained-60s-landscape", false},
		{"explained_60s", "9:16", "explained-60s-portrait", false},
		{"myth_vs_fact", "16:9", "myth-vs-fact-landscape", false},
		{"top_n", "9:16", "top-n-portrait", false},
		{"", "16:9", "", true},
		{"explained_60s", "4:3", "", true},
	}
	for _, tc := range tests {
		got, err := CompositionID(tc.format, tc.orient)
		if tc.err {
			if err == nil {
				t.Fatalf("%s/%s: want error", tc.format, tc.orient)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("%s/%s: got %q %v, want %q", tc.format, tc.orient, got, err, tc.want)
		}
	}
}

func TestLoudnormArgs(t *testing.T) {
	args := LoudnormArgs("in.mp4", "out.mp4")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "loudnorm=I=-14") {
		t.Fatalf("missing −14 LUFS filter: %v", args)
	}
	if args[len(args)-1] != "out.mp4" {
		t.Fatalf("out path = %q", args[len(args)-1])
	}
	if LoudnessLUFS != -14 {
		t.Fatalf("LoudnessLUFS = %v, want -14", LoudnessLUFS)
	}
}

func TestWriteSRT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subs.srt")
	words := []WordTiming{
		{Word: "Hello", Start: 0, End: 0.4},
		{Word: "world", Start: 0.4, End: 0.9},
		{Word: "again", Start: 2.0, End: 2.5}, // gap → new cue
	}
	if err := WriteSRT(words, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "00:00:00,000 --> 00:00:00,900") {
		t.Fatalf("missing first cue times:\n%s", s)
	}
	if !strings.Contains(s, "Hello world") {
		t.Fatalf("missing first cue text:\n%s", s)
	}
	if !strings.Contains(s, "00:00:02,000 --> 00:00:02,500") {
		t.Fatalf("missing second cue:\n%s", s)
	}
	if !strings.Contains(s, "\n2\n") {
		t.Fatalf("want two cues:\n%s", s)
	}
}

func TestWriteSRT_emptyPath(t *testing.T) {
	if err := WriteSRT(nil, ""); err == nil {
		t.Fatal("want error")
	}
}

func TestDigestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, n, err := DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("size=%d", n)
	}
	// echo -n hello | sha256sum
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sha != want {
		t.Fatalf("sha=%s want %s", sha, want)
	}
}

func TestWordsToRemotion(t *testing.T) {
	got := WordsToRemotion([]WordTiming{{Word: "Hi", Start: 1.5, End: 2.25}})
	if len(got) != 1 || got[0].StartMs != 1500 || got[0].EndMs != 2250 || got[0].Word != "Hi" {
		t.Fatalf("%+v", got)
	}
}

func TestStagePublicAndRemove(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, abs, err := StagePublic(root, "job1", src, "clip.png")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "jobs/job1/clip.png" {
		t.Fatalf("rel=%q", rel)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStagedPublic(root, "job1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "public", "jobs", "job1")); !os.IsNotExist(err) {
		t.Fatalf("staged dir still present: %v", err)
	}
}

func TestTools_NormalizeLoudnessAndMux_fakeRunner(t *testing.T) {
	dir := t.TempDir()
	inVid := filepath.Join(dir, "v.mp4")
	inAud := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(inVid, []byte("vid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inAud, []byte("aud"), 0o644); err != nil {
		t.Fatal(err)
	}

	var sawLoudnorm, sawMux bool
	tools := &Tools{
		FFmpeg: "ffmpeg",
		Run: func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
			_ = ctx
			_ = dir
			_ = env
			if name != "ffmpeg" {
				t.Fatalf("bin=%s", name)
			}
			out := args[len(args)-1]
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "loudnorm=") {
				sawLoudnorm = true
				if !strings.Contains(joined, "I=-14") {
					t.Fatalf("args=%v", args)
				}
			}
			if strings.Contains(joined, "-map") {
				sawMux = true
			}
			return nil, nil, os.WriteFile(out, []byte("out"), 0o644)
		},
	}

	loudOut := filepath.Join(dir, "loud.mp4")
	if err := tools.NormalizeLoudness(context.Background(), inVid, loudOut); err != nil {
		t.Fatal(err)
	}
	muxOut := filepath.Join(dir, "mux.mp4")
	if err := tools.MuxAV(context.Background(), inVid, inAud, muxOut); err != nil {
		t.Fatal(err)
	}
	if !sawLoudnorm || !sawMux {
		t.Fatalf("sawLoudnorm=%v sawMux=%v", sawLoudnorm, sawMux)
	}
}

func TestTools_TTS_fakeRunner(t *testing.T) {
	mtDir := t.TempDir()
	jobDir := t.TempDir()
	wav := filepath.Join(jobDir, "voice.wav")
	tools := &Tools{
		UV:            "uv",
		MediaToolsDir: mtDir,
		Run: func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
			_ = ctx
			_ = env
			if name != "uv" || dir != mtDir {
				return nil, nil, errors.New("bad invoke")
			}
			if len(args) < 6 || args[0] != "run" || args[2] != "tts" {
				return nil, nil, errors.New("bad args")
			}
			outJSON := args[len(args)-1]
			body := `{"wav_path":"` + filepath.ToSlash(wav) + `","sample_rate":24000,"duration_sec":1.0,"lang":"en","voice":"af_heart","words":[{"word":"Hi","start":0,"end":0.5}]}`
			if err := os.WriteFile(wav, []byte("RIFF"), 0o644); err != nil {
				return nil, nil, err
			}
			return nil, nil, os.WriteFile(outJSON, []byte(body), 0o644)
		},
	}
	res, err := tools.TTS(context.Background(), TTSRequest{Text: "Hi", Lang: "en", OutWAV: wav})
	if err != nil {
		t.Fatal(err)
	}
	if res.DurationSec != 1 || len(res.Words) != 1 || res.Words[0].Word != "Hi" {
		t.Fatalf("%+v", res)
	}
}

func TestTools_RemotionRender_fakeRunner(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(t.TempDir(), "long.mp4")
	props := filepath.Join(t.TempDir(), "props.json")
	if err := os.WriteFile(props, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := &Tools{
		Node:         "node",
		RemotionRoot: root,
		Run: func(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, []byte, error) {
			_ = ctx
			_ = env
			if name != "node" || dir != root {
				return nil, nil, errors.New("bad invoke")
			}
			if len(args) < 5 || args[1] != "render" {
				return nil, nil, errors.New("bad args: " + strings.Join(args, " "))
			}
			return nil, nil, os.WriteFile(args[4], []byte("mp4"), 0o644)
		},
	}
	if err := tools.RemotionRender(context.Background(), "explained-60s-landscape", props, out, false); err != nil {
		t.Fatal(err)
	}
}
