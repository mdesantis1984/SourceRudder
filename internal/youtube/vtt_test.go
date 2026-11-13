package youtube

import "testing"

func TestVTTToText(t *testing.T) {
	vtt := "WEBVTT\nKind: captions\nLanguage: es\n\n" +
		"NOTE this is a comment\nspanning two lines\n\n" +
		"00:00:00.000 --> 00:00:02.000 align:start position:0%\n" +
		"hola<00:00:00.500><c> mundo</c>\n\n" +
		"00:00:02.000 --> 00:00:02.010 align:start position:0%\n" +
		"hola mundo\n \n\n" +
		"00:00:02.010 --> 00:00:04.000 align:start position:0%\n" +
		"hola mundo\n" +
		"esto<00:00:02.500><c> es</c><00:00:03.000><c> Tom &amp; Jerry</c>\n\n" +
		"00:00:04.000 --> 00:00:04.010 align:start position:0%\n" +
		"esto es Tom &amp; Jerry\n\n" +
		"7\n00:00:05.000 --> 00:00:06.000\n" +
		"<v Speaker>dice &quot;adiós&quot;&nbsp;ya</v>\n"
	got := VTTToText(vtt)
	want := "hola mundo\nesto es Tom & Jerry\ndice \"adiós\" ya"
	if got != want {
		t.Fatalf("VTTToText mismatch\n got: %q\nwant: %q", got, want)
	}
}
