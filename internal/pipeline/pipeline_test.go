package pipeline

import (
	"testing"

	"gosintify/internal/spotify"
)

func TestNormTitleKeepsLettersAndDigits(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Back in Time (feat. X)", "backintimefeatx"},
		{"AC/DC", "acdc"},
		{"11:59", "1159"},
	} {
		if got := normTitle(tc.in); got != tc.want {
			t.Errorf("normTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBaseTitleDropsSuffixes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Don't Stop the Party (feat. TJR)", "Don't Stop the Party"},
		{"Back in Time - featured in \"Men In Black 3\"", "Back in Time"},
		{"Thriller [Remastered]", "Thriller"},
		{"Solo", "Solo"},
	} {
		if got := baseTitle(tc.in); got != tc.want {
			t.Errorf("baseTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A file name the peer used has to land on the right row: "Billie Jean
// (Underground Mix)" is not "Billie Jean".
func TestAssignFilesMatchesLongestTitle(t *testing.T) {
	tracks := []spotify.Track{
		{Artist: "Michael Jackson", Title: "Billie Jean"},
		{Artist: "Michael Jackson", Title: "Thriller"},
	}
	op := &Options{Event: func(Event) {}}
	tr := newTracker(op, tracks)
	got := tr.assignFiles([]string{
		"/x/01 - Billie Jean.mp3",
		"/x/02 - Billie Jean (Underground Mix).mp3",
		"/x/03 - Thriller.mp3",
		"/x/04 - Bonus Track.mp3",
	})
	if got["/x/01 - Billie Jean.mp3"] != 0 {
		t.Errorf("Billie Jean went to %d", got["/x/01 - Billie Jean.mp3"])
	}
	if got["/x/03 - Thriller.mp3"] != 1 {
		t.Errorf("Thriller went to %d", got["/x/03 - Thriller.mp3"])
	}
	if i, ok := got["/x/04 - Bonus Track.mp3"]; ok {
		t.Errorf("the bonus track took track %d", i)
	}
}

func TestTrackStatesGoToThePage(t *testing.T) {
	var got []Event
	tracks := []spotify.Track{{Artist: "A", Title: "One"}, {Artist: "B", Title: "Two"}}
	op := &Options{Event: func(e Event) { got = append(got, e) }}
	tr := newTracker(op, tracks)
	op.track = tr

	if i := tr.findLocked("A", "One"); i != 0 {
		t.Fatalf("find returned %d", i)
	}
	tr.set(0, StateDownloading, Event{Peer: "peer"})
	if tr.state[0] != StateDownloading {
		t.Errorf("state = %q", tr.state[0])
	}
	// A finished track stays done: a later attempt must not overwrite it.
	tr.set(0, StateDone, Event{})
	tr.set(0, StateFailed, Event{})
	if tr.state[0] != StateDone {
		t.Errorf("a done track went back to %q", tr.state[0])
	}
	if len(got) == 0 || got[0].Ev != "track" || got[0].I == nil || *got[0].I != 0 {
		t.Fatalf("events = %+v", got)
	}
}

func TestKindOfOptions(t *testing.T) {
	if k := (&Options{}).Kind(); k != "link" {
		t.Errorf("Kind() = %q", k)
	}
	op := &Options{Collection: &spotify.Collection{Kind: "album"}}
	if k := op.Kind(); k != "album" {
		t.Errorf("Kind() = %q", k)
	}
}
