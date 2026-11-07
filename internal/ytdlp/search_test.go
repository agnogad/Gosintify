package ytdlp

import "testing"

func TestScorePrefersTheRightSong(t *testing.T) {
	// The album, artist and length of the track being searched for.
	const title, artist, length = "Back in Time", "Pitbull", 207

	good := Candidate{Title: "Pitbull - Back in Time (featured in Men In Black 3)", Artist: "Pitbull", Album: "Global Warming", Duration: 207}
	lyrics := Candidate{Title: "Pitbull - Back in Time (Lyrics)", Artist: "Golden Network", Duration: 211}
	karaoke := Candidate{Title: "Back in Time (Karaoke Version)", Artist: "Karaoke Hits", Duration: 210}
	long := Candidate{Title: "Pitbull - Back in Time (12 hour loop)", Artist: "Pitbull", Duration: 43000}

	goodScore := score(good, title, artist, length)
	if goodScore < 12 {
		t.Fatalf("the right song scored %.1f, want at least 12", goodScore)
	}
	for _, other := range []Candidate{lyrics, karaoke, long} {
		if s := score(other, title, artist, length); s >= goodScore {
			t.Errorf("%q scored %.1f, not below the right song's %.1f", other.Title, s, goodScore)
		}
	}
}

func TestIsBadVideoRejectsNoise(t *testing.T) {
	for _, tc := range []struct{ video, want string }{
		{"Song (Official Video)", "Song"},
		{"Song (Lyrics)", "Song"},
		{"Song (Karaoke)", "Song"},
		{"Song (Live)", "Song"},
		{"Song (Nightcore)", "Song"},
	} {
		if !isBadVideo(tc.video, tc.want) {
			t.Errorf("accepted %q for %q", tc.video, tc.want)
		}
	}
	// The wanted title itself may say "official video"; that is not noise.
	if isBadVideo("Song (Official Video)", "Song (Official Video)") {
		t.Error("rejected the wanted title's own words")
	}
}

func TestSafeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`AC/DC - Back In Black`, "AC DC - Back In Black"},
		{`What? \ "quoted": <tag> | pipe`, "What quoted tag pipe"},
		{"  spaced  ", "spaced"},
	} {
		if got := SafeName(tc.in); got != tc.want {
			t.Errorf("SafeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// norm drops combining marks, so an accented letter loses its accent, and
// turns everything else that is not a letter, digit or space into a space.
func TestNormFoldsPunctuation(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Déjà Vu (feat. Someone)", "d j vu feat someone"},
		{"Back in Time - Remastered", "back in time remastered"},
		{"  ACDC  2024  ", "acdc 2024"},
	} {
		if got := norm(tc.in); got != tc.want {
			t.Errorf("norm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSearchEntryRejectsBrowseResults(t *testing.T) {
	for _, entry := range []searchEntry{
		{ID: "UCtHVFWQtBzPt8m0J9e59TSQ", URL: "https://music.youtube.com/browse/UCtHVFWQtBzPt8m0J9e59TSQ", IEKey: "YoutubeTab"},
		{ID: "MPREb_RrNUGwNPHsE", IEKey: "YoutubeTab"},
		{ID: "aaaaaaaaaaa", URL: "https://music.youtube.com/browse/aaaaaaaaaaa"},
		{ID: "aaaaaaaaaaa", URL: "https://example.test/watch?v=aaaaaaaaaaa"},
		{ID: "aaaaaaaaaaa", URL: "https://youtube.com/watch?v=bbbbbbbbbbb"},
	} {
		if entry.isVideo() {
			t.Errorf("accepted a non-video: %+v", entry)
		}
	}
	if !(searchEntry{ID: "aaaaaaaaaaa", URL: "https://music.youtube.com/watch?v=aaaaaaaaaaa", IEKey: "Youtube"}).isVideo() {
		t.Fatal("rejected a valid video")
	}
}
