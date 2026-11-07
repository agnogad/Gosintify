package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"gosintify/internal/appstore"
	"gosintify/internal/pipeline"
	"gosintify/internal/slsk"
	"gosintify/internal/spotify"
	"gosintify/internal/ytdlp"
)

// Pipeline events reach the page with this name.
var pipelineEvent = mygo.NewEvent[pipeline.Event]("pipeline")

// Job states sent to the page.
const (
	phaseIdle     = "idle"
	phaseSpotify  = "spotify"
	phaseSoulseek = "soulseek"
	phaseYoutube  = "youtube"
	phaseConvert  = "convert"
	phaseDone     = "done"
	phaseStopped  = "stopped"
	phaseFailed   = "failed"
	phaseChoosing = "choosing"
)

// job is one running (or finished) download.
type job struct {
	mu       sync.Mutex
	phase    string
	err      string
	stop     chan struct{}
	stopOnce sync.Once
	cancel   context.CancelFunc
	choose   chan []int

	// What the window shows while it works and after it finishes.
	name   string
	kind   string
	outDir string
	link   string
	// tracks is the collection, with the state of every track.
	tracks []pipeline.TrackEvent
	states []string
	bytes  []int64
	totals []int64
	source []string // "soulseek" or "youtube" once done
	peers  map[string]bool
	files  []pipeline.Event

	started time.Time
	speed   float64 // bytes per second, smoothed
	lastAt  time.Time
	lastB   int64

	convertDone  int
	convertTotal int
}

// Api is the surface the frontend calls. Every exported method becomes a
// typed TypeScript function in src/mygo.ts.
type Api struct {
	win  *mygo.Window
	mu   sync.Mutex
	job  *job
	auth *spotify.Auth
}

// emit records a pipeline event in the running job and forwards it to the
// page. The recorded state is what GetState returns, so a page that reloads
// (or the poster, drawn after the run) still shows the whole run.
func (a *Api) emit(e pipeline.Event) {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j != nil {
		j.apply(e)
	}
	if a.win != nil {
		_ = pipelineEvent.Emit(a.win, e)
	}
}

// apply folds one pipeline event into the job state.
func (j *job) apply(e pipeline.Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch e.Ev {
	case "collection":
		j.name = e.Name
		j.kind = e.Kind
		if e.Artist != "" && j.kind == "track" {
			j.name = e.Name
		}
		j.tracks = append([]pipeline.TrackEvent(nil), e.Tracks...)
		j.states = make([]string, len(j.tracks))
		j.bytes = make([]int64, len(j.tracks))
		j.totals = make([]int64, len(j.tracks))
		j.source = make([]string, len(j.tracks))
		for i := range j.states {
			j.states[i] = pipeline.StatePending
		}
	case "track":
		if e.I != nil && *e.I >= 0 && *e.I < len(j.states) {
			i := *e.I
			j.states[i] = e.State
			if e.Peer != "" {
				j.totals[i] = e.Total
				if j.peers == nil {
					j.peers = map[string]bool{}
				}
				j.peers[e.Peer] = true
			}
			if e.Source != "" {
				j.source[i] = e.Source
			}
			if e.BitRate != 0 || e.Path != "" || e.URL != "" {
				j.files = append(j.files, e)
			}
		}
	case "bytes":
		if e.I != nil && *e.I >= 0 && *e.I < len(j.bytes) {
			i := *e.I
			now := time.Now()
			// A smoothed rate, so the number in the header does not jump.
			if !j.lastAt.IsZero() {
				dt := now.Sub(j.lastAt).Seconds()
				if dt > 0.2 {
					delta := float64(e.Bytes - j.lastB)
					if delta >= 0 {
						if j.speed == 0 {
							j.speed = delta / dt
						} else {
							j.speed = 0.7*j.speed + 0.3*(delta/dt)
						}
					}
					j.lastAt = now
					j.lastB = e.Bytes
				}
			} else {
				j.lastAt = now
				j.lastB = e.Bytes
			}
			j.bytes[i] = e.Bytes
			j.totals[i] = e.Total
		}
	case "file":
		j.files = append(j.files, e)
	case "convert":
		j.convertDone = e.Done
		j.convertTotal = e.Count
	case "phase":
		if e.Phase == "convert" {
			j.phase = phaseConvert
		}
	}
}

// ---- settings -------------------------------------------------------------

// Settings is what the window remembers between runs.
type Settings struct {
	OutputDir    string `json:"outputDir"`
	Bitrate      string `json:"bitrate"`
	Fallback     bool   `json:"fallback"`
	WantSoulseek bool   `json:"wantSoulseek"`
	SoulseekUser string `json:"soulseekUser"`
	ClientID     string `json:"clientId"`
	Volume       int    `json:"volume"`
	Connected    bool   `json:"connected"`
	SpotifyUser  string `json:"spotifyUser"`
	ConfigDir    string `json:"configDir"`
}

// GetSettings returns the saved settings.
func (a *Api) GetSettings() Settings {
	s := appstore.LoadSettings()
	out := Settings{
		OutputDir:    s.OutputDir,
		Bitrate:      s.Bitrate,
		Fallback:     s.Fallback,
		WantSoulseek: s.WantSoulseek,
		SoulseekUser: s.SoulseekUser,
		ClientID:     s.ClientID,
		Volume:       s.Volume,
		ConfigDir:    appstore.Dir(),
	}
	if auth := a.spotifyAuth(); auth != nil && auth.LoggedIn() {
		out.Connected = true
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if name, err := auth.Profile(ctx); err == nil {
			out.SpotifyUser = name
		}
	}
	return out
}

// SaveSettings stores the settings.
func (a *Api) SaveSettings(in Settings) error {
	bitrate := in.Bitrate
	if bitrate == "" {
		bitrate = "320"
	}
	return appstore.SaveSettings(appstore.Settings{
		OutputDir:    in.OutputDir,
		Bitrate:      bitrate,
		Fallback:     in.Fallback,
		WantSoulseek: in.WantSoulseek,
		SoulseekUser: in.SoulseekUser,
		ClientID:     in.ClientID,
		Volume:       in.Volume,
	})
}

// ChooseFolder asks for the export folder with the native picker. It returns
// "" when the user cancels.
func (a *Api) ChooseFolder() (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      a.win,
		Title:       "Choose the export folder",
		Directory:   true,
		ButtonLabel: "Choose",
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}
	return paths[0], nil
}

// OpenFolder shows a folder in the file manager.
func (a *Api) OpenFolder(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("the folder is empty")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return mygo.Shell.OpenPath(path)
}

// OpenLink opens a web page in the browser.
func (a *Api) OpenLink(url string) error {
	return mygo.Shell.OpenExternal(url)
}

// ---- tools ----------------------------------------------------------------

// ToolsState describes which external tools were found.
type ToolsState struct {
	Sockseek bool `json:"sockseek"`
	Ytdlp    bool `json:"ytdlp"`
	Ffmpeg   bool `json:"ffmpeg"`
	Ffprobe  bool `json:"ffprobe"`
}

// CheckTools checks every external tool the app runs.
func (a *Api) CheckTools() ToolsState {
	return ToolsState{
		Sockseek: toolFound("sockseek"),
		Ytdlp:    toolFound("yt-dlp"),
		Ffmpeg:   toolFound("ffmpeg"),
		Ffprobe:  toolFound("ffprobe"),
	}
}

func toolFound(name string) bool {
	_, err := findTool(name)
	return err == nil
}

// ---- Soulseek account -----------------------------------------------------

// SoulseekLogin signs in to Soulseek once to check or create an account.
// A new name creates the account.
func (a *Api) SoulseekLogin(user, password string) (string, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return "", errors.New("enter a Soulseek username")
	}
	if password == "" {
		return "", errors.New("enter the password")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := slsk.Login(ctx, slsk.Server, user, password)
	if err != nil {
		return "", err
	}
	if !res.OK {
		msg := res.Reason
		if res.Detail != "" {
			msg += ": " + res.Detail
		}
		if res.Reason == "INVALIDPASS" {
			msg = "That name is taken with another password, or the password is wrong."
		}
		return "", errors.New(msg)
	}
	s := appstore.LoadSettings()
	s.SoulseekUser = user
	s.WantSoulseek = true
	_ = appstore.SaveSettings(s)
	return "Signed in as " + user, nil
}

// ---- Spotify --------------------------------------------------------------

func (a *Api) spotifyAuth() *spotify.Auth {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.auth == nil {
		a.auth = spotify.NewAuth(appstore.LoadSettings().ClientID)
	}
	return a.auth
}

// ConnectSpotify opens the browser for the read-only Spotify login.
func (a *Api) ConnectSpotify(clientID string) (string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "", errors.New("paste your Spotify app Client ID first")
	}
	s := appstore.LoadSettings()
	s.ClientID = clientID
	_ = appstore.SaveSettings(s)
	auth := spotify.NewAuth(clientID)
	a.mu.Lock()
	a.auth = auth
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := auth.Login(ctx, func(u string) error { return mygo.Shell.OpenExternal(u) }); err != nil {
		return "", err
	}
	name, err := auth.Profile(context.Background())
	if err != nil || name == "" {
		return "Connected to Spotify", nil
	}
	return "Signed in as " + name, nil
}

// DisconnectSpotify forgets the stored Spotify session.
func (a *Api) DisconnectSpotify() error {
	auth := a.spotifyAuth()
	if auth != nil {
		auth.Logout()
	}
	return nil
}

// LibraryItem is a playlist or album of the user's library.
type LibraryItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Count    int    `json:"count"`
	Kind     string `json:"kind"`
	Cover    string `json:"cover,omitempty"`
}

// Library is the signed-in user's Spotfy library.
type Library struct {
	User      string        `json:"user"`
	Playlists []LibraryItem `json:"playlists"`
	Albums    []LibraryItem `json:"albums"`
	Liked     int           `json:"liked"`
	LikedAll  bool          `json:"likedAll"`
	Errors    []string      `json:"errors,omitempty"`
}

// GetLibrary loads the playlists, albums and liked songs of the account.
func (a *Api) GetLibrary() (Library, error) {
	auth := a.spotifyAuth()
	if auth == nil || !auth.LoggedIn() {
		return Library{}, errors.New("Spotify is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var out Library
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		if name, err := auth.Profile(ctx); err == nil {
			out.User = name
		} else {
			out.Errors = append(out.Errors, "profile: "+err.Error())
		}
	}()
	go func() {
		defer wg.Done()
		items, err := auth.Playlists(ctx)
		if err != nil {
			out.Errors = append(out.Errors, "playlists: "+err.Error())
			return
		}
		for _, it := range items {
			out.Playlists = append(out.Playlists, LibraryItem{ID: it.ID, Name: it.Name, Subtitle: it.Subtitle, Count: it.Count, Kind: "playlist"})
		}
	}()
	go func() {
		defer wg.Done()
		items, err := auth.Albums(ctx)
		if err != nil {
			out.Errors = append(out.Errors, "albums: "+err.Error())
			return
		}
		for _, it := range items {
			out.Albums = append(out.Albums, LibraryItem{ID: it.ID, Name: it.Name, Subtitle: it.Subtitle, Count: it.Count, Kind: "album"})
		}
	}()
	wg.Wait()
	if tracks, err := auth.LikedTracks(ctx); err == nil {
		out.Liked = len(tracks)
		out.LikedAll = true
	}
	if out.Playlists == nil {
		out.Playlists = []LibraryItem{}
	}
	if out.Albums == nil {
		out.Albums = []LibraryItem{}
	}
	return out, nil
}

// ---- YouTube Music search -------------------------------------------------

// SearchResult is one YouTube Music result the user can download.
type SearchResult struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Duration int    `json:"duration"`
	Thumb    string `json:"thumb"`
}

// SearchYouTubeMusic searches YouTube Music for a free-text query.
func (a *Api) SearchYouTubeMusic(query string) ([]SearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cands, err := ytdlp.SearchMusic(ctx, query, 20)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(cands))
	for _, c := range cands {
		out = append(out, SearchResult{
			ID:       c.ID,
			URL:      c.URL,
			Title:    c.Title,
			Artist:   c.Artist,
			Duration: c.Duration,
			Thumb:    "https://i.ytimg.com/vi/" + c.ID + "/mqdefault.jpg",
		})
	}
	return out, nil
}

// StartTrackDownload downloads one YouTube Music result straight to the
// export folder.
func (a *Api) StartTrackDownload(url, title string) error {
	s := appstore.LoadSettings()
	dest := s.OutputDir
	go func() {
		ctx := context.Background()
		name := ytdlp.SafeName(title)
		name = strings.Trim(name, "-_. ")
		if name == "" {
			name = "track"
		}
		a.emit(pipeline.Event{Ev: "phase", Phase: phaseYoutube})
		a.emit(pipeline.Event{Ev: "log", Line: "Downloading " + title})
		err := ytdlp.Download(ctx, ytdlp.Candidate{URL: url, Title: title}, dest, name, s.Bitrate+"K")
		if err != nil {
			a.emit(pipeline.Event{Ev: "error", Message: err.Error()})
			a.setPhase(phaseFailed, err.Error())
			return
		}
		a.emit(pipeline.Event{Ev: "log", Line: "Saved " + filepath.Join(dest, name+".mp3")})
		a.setPhase(phaseDone, "")
	}()
	return nil
}

// ---- downloading ----------------------------------------------------------

// StartInput reads a Spotify link and downloads it.
func (a *Api) StartInput(input string, autoselectAll bool) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return errors.New("paste a Spotify playlist, album, or track link")
	}
	return a.start(&pipeline.Options{Input: input}, autoselectAll)
}

// StartLibraryItem downloads a playlist or album of the user's library.
func (a *Api) StartLibraryItem(kind, id, name, artist, link string, autoselectAll bool) error {
	auth := a.spotifyAuth()
	if auth == nil || !auth.LoggedIn() {
		return errors.New("Spotify is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	coll := &spotify.Collection{Kind: kind, Name: name, Artist: artist, ID: id}
	switch kind {
	case "album":
		tracks, err := auth.AlbumTracks(ctx, id)
		if err != nil {
			if c, e := spotify.Extract("https://open.spotify.com/album/" + id); e == nil {
				tracks, err = c.Tracks, nil
				coll.Name, coll.Artist, coll.Cover = c.Name, c.Artist, c.Cover
			}
		}
		if err != nil {
			return err
		}
		coll.Tracks = tracks
	default:
		tracks, err := auth.PlaylistTracks(ctx, id)
		if err != nil || len(tracks) == 0 {
			if c, e := spotify.Extract("https://open.spotify.com/playlist/" + id); e == nil {
				tracks, err = c.Tracks, nil
				coll.Name, coll.Cover = c.Name, c.Cover
			}
		}
		if err != nil {
			return err
		}
		coll.Tracks = tracks
	}
	if len(coll.Tracks) == 0 {
		return fmt.Errorf("%s has no tracks", name)
	}
	return a.start(&pipeline.Options{Collection: coll, Input: link}, autoselectAll)
}

// StartLiked downloads every liked song.
func (a *Api) StartLiked(autoselectAll bool) error {
	auth := a.spotifyAuth()
	if auth == nil || !auth.LoggedIn() {
		return errors.New("Spotify is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	tracks, err := auth.LikedTracks(ctx)
	if err != nil {
		return err
	}
	if len(tracks) == 0 {
		return errors.New("there are no liked songs")
	}
	coll := &spotify.Collection{Kind: "playlist", Name: "Liked Songs", Tracks: tracks}
	return a.start(&pipeline.Options{Collection: coll, Input: "liked-songs"}, autoselectAll)
}

func (a *Api) start(base *pipeline.Options, autoselectAll bool) error {
	a.mu.Lock()
	if a.job != nil {
		// A finished run is only kept so the poster can be drawn; a new one
		// replaces it. A run in flight is not interrupted.
		if !a.job.finished() {
			a.mu.Unlock()
			return errors.New("a download is already running")
		}
	}
	s := appstore.LoadSettings()
	j := &job{
		phase:   phaseSpotify,
		stop:    make(chan struct{}),
		choose:  make(chan []int, 1),
		outDir:  s.OutputDir,
		link:    base.Input,
		started: time.Now(),
		peers:   map[string]bool{},
	}
	a.job = j
	a.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	j.mu.Lock()
	j.cancel = cancel
	j.mu.Unlock()

	opts := *base
	if toolFound("yt-dlp") {
		yd, _ := findTool("yt-dlp")
		ytdlp.Binary = yd
	}
	if p, err := findTool("sockseek"); err == nil {
		opts.SockseekPath = p
	}
	if p, err := findTool("ffmpeg"); err == nil {
		opts.FfmpegPath = p
	}
	if p, err := findTool("ffprobe"); err == nil {
		opts.FfprobePath = p
	}
	opts.OutputDir = s.OutputDir
	opts.Bitrate = s.Bitrate
	opts.UseFallback = s.Fallback
	opts.SoulseekUser = s.SoulseekUser
	opts.SkipSoulseek = !s.WantSoulseek || s.SoulseekUser == ""
	opts.Stop = j.stop
	opts.Event = a.emit
	opts.Log = nil
	if !autoselectAll {
		opts.Select = func(ctx context.Context, n int) ([]int, error) {
			a.setPhase(phaseChoosing, "")
			select {
			case only := <-j.choose:
				return only, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	go func() {
		// The finished job stays attached: the poster and the track list are
		// drawn from it. A new run replaces it, and so does Cancel.
		defer cancel()
		res, err := pipeline.Run(ctx, &opts)
		if err != nil {
			if j.stopped() {
				a.setPhase(phaseStopped, "")
				a.emit(pipeline.Event{Ev: "error", Message: "stopped"})
				return
			}
			a.setPhase(phaseFailed, err.Error())
			a.emit(pipeline.Event{Ev: "error", Message: err.Error()})
			return
		}
		a.setPhase(phaseDone, "")
		_ = pipeline.WriteReport(res)
		kind := res.Kind
		if kind == "" {
			kind = opts.Kind()
		}
		_ = appstore.AddHistory(appstore.HistoryEntry{
			Kind:    kind,
			Link:    opts.Input,
			Name:    res.Collection,
			Files:   res.Files,
			Missing: res.Missing,
			Dir:     res.OutputDir,
		})
	}()
	return nil
}

// ChooseTracks answers the track picker: the indexes to download, or every
// index to take them all.
func (a *Api) ChooseTracks(indexes []int) error {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j == nil {
		return errors.New("no download is waiting for a choice")
	}
	select {
	case j.choose <- indexes:
		a.setPhase(phaseSoulseek, "")
		return nil
	default:
		return errors.New("the track list was already answered")
	}
}

// Stop ends the running download. The finished files are still converted.
func (a *Api) Stop() error {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j == nil {
		return errors.New("nothing is downloading")
	}
	j.stopOnce.Do(func() { close(j.stop) })
	return nil
}

// Cancel aborts the running download at once.
func (a *Api) Cancel() error {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j == nil {
		return errors.New("nothing is downloading")
	}
	j.mu.Lock()
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Cancel also lets the window put the finished job away, so a new link
	// starts on a clean poster.
	a.mu.Lock()
	if a.job == j && j.finished() {
		a.job = nil
	}
	a.mu.Unlock()
	return nil
}

// finished reports whether the job has nothing left to do.
func (j *job) finished() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.phase == phaseDone || j.phase == phaseFailed || j.phase == phaseStopped || j.phase == phaseIdle
}

func (j *job) stopped() bool {
	select {
	case <-j.stop:
		return true
	default:
		return false
	}
}

// State is the current phase of the downloader.
// State is everything the window draws. The page keeps no copy of the run:
// the poster, the track list and the footer all come from here.
type State struct {
	Phase string `json:"phase"`
	Error string `json:"error,omitempty"`
	Busy  bool   `json:"busy"`

	// The collection of the run.
	Name   string       `json:"name"`
	Kind   string       `json:"kind"`
	Link   string       `json:"link"`
	OutDir string       `json:"outDir"`
	Tracks []TrackState `json:"tracks"`

	// Progress, for the bar and the counters.
	Progress float64  `json:"progress"`
	Finished int      `json:"finished"`
	Done     int      `json:"done"`
	Missing  int      `json:"missing"`
	Speed    float64  `json:"speed"`
	Peers    []string `json:"peers,omitempty"`

	// Rolling totals of the finished run, for the poster footer.
	FromSoulseek int  `json:"fromSoulseek"`
	FromYouTube  int  `json:"fromYoutube"`
	Stopped      bool `json:"stopped"`
	ConvertDone  int  `json:"convertDone"`
	ConvertTotal int  `json:"convertTotal"`
}

// TrackState is one row of the track list.
type TrackState struct {
	Artist   string `json:"artist"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Album    string `json:"album,omitempty"`
	State    string `json:"state"`
	Bytes    int64  `json:"bytes"`
	Total    int64  `json:"total"`
	Peer     string `json:"peer,omitempty"`
	Source   string `json:"source,omitempty"`
	Path     string `json:"path,omitempty"`
	URL      string `json:"url,omitempty"`
	BitRate  int    `json:"bitrate,omitempty"`
}

// GetState returns whether a download is running and in which phase.
func (a *Api) GetState() State {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j == nil {
		return State{Phase: phaseIdle}
	}
	return j.snapshot()
}

// snapshot is the state the page draws, taken under the job lock.
func (j *job) snapshot() State {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := State{
		Phase:        j.phase,
		Error:        j.err,
		Busy:         true,
		Name:         j.name,
		Kind:         j.kind,
		Link:         j.link,
		OutDir:       j.outDir,
		Progress:     j.progressLocked(),
		Speed:        j.speed,
		Done:         j.countLocked(pipeline.StateDone),
		Missing:      j.countLocked(pipeline.StateMissing),
		Stopped:      j.phase == phaseStopped,
		ConvertDone:  j.convertDone,
		ConvertTotal: j.convertTotal,
	}
	peers := make([]string, 0, len(j.peers))
	for p := range j.peers {
		peers = append(peers, p)
	}
	st.Peers = peers
	for i, t := range j.tracks {
		ts := TrackState{
			Artist:   t.Artist,
			Title:    t.Title,
			Duration: t.Duration,
			Album:    t.Album,
			State:    j.states[i],
			Bytes:    j.bytes[i],
			Total:    j.totals[i],
			Source:   j.source[i],
		}
		for _, f := range j.files {
			if f.I != nil && *f.I == i {
				if f.Path != "" {
					ts.Path = f.Path
				}
				if f.URL != "" {
					ts.URL = f.URL
				}
				if f.BitRate != 0 {
					ts.BitRate = f.BitRate
				}
				if f.Peer != "" {
					ts.Peer = f.Peer
				}
			}
		}
		if ts.State == "" {
			ts.State = pipeline.StatePending
		}
		if ts.State == pipeline.StateDone {
			if ts.Source == "youtube" {
				st.FromYouTube++
			} else {
				st.FromSoulseek++
			}
		}
		st.Tracks = append(st.Tracks, ts)
	}
	for _, t := range st.Tracks {
		if t.State == pipeline.StateDone || t.State == pipeline.StateMissing || t.State == pipeline.StateSkipped {
			st.Finished++
		}
	}
	return st
}

func (j *job) countLocked(state string) int {
	n := 0
	for _, s := range j.states {
		if s == state {
			n++
		}
	}
	return n
}

// progressLocked is the overall 0..1 of the run: the tracks, then the mp3
// conversion, the same shape the original app shows.
func (j *job) progressLocked() float64 {
	n := len(j.tracks)
	if n == 0 {
		return 0
	}
	if j.phase == phaseDone {
		return 1
	}
	sum := 0.0
	for i := range j.tracks {
		sum += trackProgress(j.states[i], j.bytes[i], j.totals[i])
	}
	tracks := sum / float64(n)
	if j.convertTotal > 0 {
		conv := float64(j.convertDone) / float64(j.convertTotal)
		return 0.9 + 0.1*conv
	}
	return tracks * 0.9
}

// trackProgress is how far one row is, 0..1.
func trackProgress(state string, bytes, total int64) float64 {
	switch state {
	case pipeline.StateSearching:
		return 0.04
	case pipeline.StateDownloading, pipeline.StateYTDownload:
		frac := 0.0
		if total > 0 {
			frac = float64(bytes) / float64(total)
		}
		if state == pipeline.StateYTDownload && frac == 0 {
			return 0.55
		}
		return 0.06 + 0.94*frac
	case pipeline.StateFailed, pipeline.StateYTSearch:
		return 0.2
	case pipeline.StateDone, pipeline.StateMissing, pipeline.StateSkipped:
		return 1
	}
	return 0
}

func (a *Api) setPhase(phase, errMsg string) {
	a.mu.Lock()
	j := a.job
	a.mu.Unlock()
	if j == nil {
		return
	}
	j.mu.Lock()
	j.phase, j.err = phase, errMsg
	j.mu.Unlock()
	if a.win != nil {
		_ = stateEvent.Emit(a.win, State{Phase: phase, Error: errMsg, Busy: j.phase != phaseDone && j.phase != phaseFailed && j.phase != phaseStopped})
	}
}

// Env returns the config folder and the export folder, for the About line.
func (a *Api) Env() map[string]string {
	s := appstore.LoadSettings()
	return map[string]string{
		"outputDir": s.OutputDir,
		"configDir": appstore.Dir(),
		"home":      homeDir(),
	}
}

// stateEvent tells the page when the phase changes.
var stateEvent = mygo.NewEvent[State]("state")

// ---- history --------------------------------------------------------------

// GetHistory returns the finished downloads, newest first.
func (a *Api) GetHistory() []appstore.HistoryEntry {
	return appstore.History()
}

// ClearHistory forgets every finished download.
func (a *Api) ClearHistory() error {
	return appstore.ClearHistory()
}
