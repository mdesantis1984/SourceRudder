package youtube

import "testing"

func TestChooseTrack(t *testing.T) {
	manual := func(langs ...string) map[string][]subFormat {
		m := map[string][]subFormat{}
		for _, l := range langs {
			m[l] = []subFormat{{Ext: "vtt"}}
		}
		return m
	}
	cases := []struct {
		name       string
		info       videoInfo
		requested  string
		wantLang   string
		wantManual bool
		wantOK     bool
	}{
		{
			name:       "manual in requested language",
			info:       videoInfo{Language: "en", Subtitles: manual("en", "es-419"), AutomaticCaptions: manual("en-orig", "es")},
			requested:  "es",
			wantLang:   "es-419",
			wantManual: true,
			wantOK:     true,
		},
		{
			name:      "auto orig of spoken language among several dubbed origs",
			info:      videoInfo{Language: "es", AutomaticCaptions: manual("ar-orig", "en-orig", "es-orig", "pt-orig", "es", "en")},
			wantLang:  "es-orig",
			wantOK:    true,
			requested: "",
		},
		{
			name:       "manual in spoken language when no language is requested",
			info:       videoInfo{Language: "es", Subtitles: manual("live_chat", "es"), AutomaticCaptions: manual("es-orig")},
			wantLang:   "es",
			wantManual: true,
			wantOK:     true,
		},
		{
			name:       "any manual when requested language is missing",
			info:       videoInfo{Language: "en", Subtitles: manual("live_chat", "fr", "de"), AutomaticCaptions: manual("es", "ja-orig")},
			requested:  "es",
			wantLang:   "de",
			wantManual: true,
			wantOK:     true,
		},
		{
			name:      "any orig when nothing else matches",
			info:      videoInfo{AutomaticCaptions: manual("ja", "pt-orig", "en-orig")},
			requested: "es",
			wantLang:  "en-orig",
			wantOK:    true,
		},
		{
			name:   "live chat only is no transcript",
			info:   videoInfo{Language: "en", Subtitles: manual("live_chat")},
			wantOK: false,
		},
		{
			name:   "no tracks",
			info:   videoInfo{Language: "en"},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := chooseTrack(&tc.info, tc.requested)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (track %+v)", ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Lang != tc.wantLang || got.Manual != tc.wantManual {
				t.Fatalf("track = %+v, want lang %q manual %v", got, tc.wantLang, tc.wantManual)
			}
		})
	}
}
