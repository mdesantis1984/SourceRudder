package youtube

import "testing"

func TestParseVideoID(t *testing.T) {
	const id = "Q-_VdeexbUI"
	videos := []string{
		"https://www.youtube.com/watch?v=" + id,
		"https://youtube.com/watch?v=" + id + "&t=42s&list=PL123",
		"http://m.youtube.com/watch?feature=share&v=" + id,
		"https://music.youtube.com/watch?v=" + id,
		"https://youtu.be/" + id,
		"https://youtu.be/" + id + "?si=abc",
		"youtu.be/" + id,
		"www.youtube.com/watch?v=" + id,
		"https://www.youtube.com/shorts/" + id,
		"https://www.youtube.com/embed/" + id + "?start=3",
		"https://www.youtube.com/live/" + id + "?feature=share",
		"https://www.youtube-nocookie.com/embed/" + id,
		"https://WWW.YouTube.com/watch?v=" + id,
		"  https://youtu.be/" + id + "  ",
		id,
	}
	for _, raw := range videos {
		got, ok := ParseVideoID(raw)
		if !ok || got != id {
			t.Errorf("ParseVideoID(%q) = %q, %v; want %q, true", raw, got, ok, id)
		}
	}

	notVideos := []string{
		"",
		"https://www.youtube.com/",
		"https://www.youtube.com/@somechannel",
		"https://www.youtube.com/@somechannel/live",
		"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv",
		"https://www.youtube.com/c/SomeName",
		"https://www.youtube.com/results?search_query=golang",
		"https://www.youtube.com/playlist?list=PL1234567890",
		"https://www.youtube.com/watch",
		"https://www.youtube.com/watch?v=short",
		"https://www.youtube.com/watch?v=" + id + "x",
		"https://youtu.be/",
		"https://example.com/watch?v=" + id,
		"https://notyoutube.com/watch?v=" + id,
		"https://youtube.com.evil.example/watch?v=" + id,
		"ftp://www.youtube.com/watch?v=" + id,
		"https://golang.org/doc",
		"hello world",
		"Q-_VdeexbU!",
	}
	for _, raw := range notVideos {
		if got, ok := ParseVideoID(raw); ok {
			t.Errorf("ParseVideoID(%q) = %q, true; want not a video", raw, got)
		}
	}
}

func TestIsVideoURLRejectsBareID(t *testing.T) {
	if IsVideoURL("Q-_VdeexbUI") {
		t.Fatal("a bare ID is not a URL; fetch_url must keep its previous behavior for it")
	}
	if !IsVideoURL("https://youtu.be/Q-_VdeexbUI") {
		t.Fatal("youtu.be URL must be detected")
	}
	if IsVideoURL("https://www.youtube.com/@somechannel") {
		t.Fatal("channel URL is not a video")
	}
}
