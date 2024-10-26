package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

const port = 1313

const pageSize = 100

const (
	errServerError = "server error"
	errNotFound    = "not found"
)

const (
	uriPrefix          = "spotify:playlist:"
	searchURLBase      = "https://www.youtube.com"
	spotifyAPIBase     = "https://api.spotify.com/v1"
	spotifyAuthAPIBase = "https://accounts.spotify.com/api"

	searchModeMusicVideo = "music video"
	searchModeLive       = "live"
	searchModeCover      = "cover"
	searchModeShit       = "shit"

	modeWeb  = "web"
	modeText = "text"
)

type config struct {
	spotifyClientID     string
	spotifyClientSecret string
	mode                string
	searchMode          string
}

func (cfg *config) validate() error {
	if cfg.spotifyClientID == "" {
		return errors.New("spotify client id is empty")
	}
	if cfg.spotifyClientSecret == "" {
		return errors.New("spotify client secret empty")
	}

	return nil
}

type videoPlaylist struct {
	cfg        config
	token      string
	tokenSetAt *time.Time
	che        map[string]videoPage
}

type tmpldata struct {
	PlaylistURI string
	Videos      []video
	ErrMessage  string
	Previous    string
	Next        string
}

type videoPage struct {
	videos  []video
	hasNext bool
}

type video struct {
	Name      string
	SearchURL string
}

type playlistRes struct {
	Items []struct {
		Track struct {
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"track"`
	} `json:"items"`
	Error struct {
		Status int `json:"status"`
	}
	// The spotify playlists api includes a next url
	// which will be non-zero if there are more songs.
	Next string `json:"next"`
}

func main() {
	cfg := config{}

	flag.StringVar(&cfg.mode, "mode", modeWeb, "app mode (web or text, default is web)")
	flag.StringVar(&cfg.searchMode, "searchMode", searchModeMusicVideo, "search mode (music video, live, cover, or shit, default is music video)")
	flag.StringVar(&cfg.spotifyClientID, "spotifyClientID", os.Getenv("SPOTIFY_CLIENT_ID"), "spotify client id")
	flag.StringVar(&cfg.spotifyClientSecret, "spotifyClientSecret", os.Getenv("SPOTIFY_CLIENT_SECRET"), "spotify client secret")
	flag.Parse()

	var err error
	if err = cfg.validate(); nil != err {
		log.Fatal(err)
	}

	log.Printf(`
		video playlister running with
		client id:     %s
		client secret: %s
		mode:          %s 
		search mode:   %s
	`,
		cfg.spotifyClientID,
		cfg.spotifyClientSecret,
		cfg.mode,
		cfg.searchMode,
	)

	var vp videoPlaylist
	vp.cfg = cfg
	vp.che = make(map[string]videoPage)

	switch cfg.mode {
	case modeWeb:
		vp.handleWeb()
	case modeText:
		vp.handleText()
	default:
		log.Fatalf("unknown mode '%s'", cfg.mode)
	}
}

func (vp *videoPlaylist) handleWeb() {
	r := mux.NewRouter()
	r.Handle("/", vp).Methods("GET", "POST")

	log.Printf("🧜‍♂️  server is running at http://localhost:%d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), r))
}

func (vp *videoPlaylist) handleText() {
	var err error

	for {
		var plURI string
		for {
			time.Sleep(500 * time.Millisecond)
			fmt.Printf("Enter a spotify playlist uri: ")

			if _, err = fmt.Scanln(&plURI); nil != err {
				log.Fatal(err)
			}

			if strings.TrimSpace(plURI) == "" {
				fmt.Println("Please enter a valid uri")
				continue
			}

			break
		}

		pageNum := 1
		for {
			offset := (pageNum - 1) * pageSize

			var songs []string
			var hasNextPage bool
			if songs, hasNextPage, err = vp.getPlaylistSongs(plURI, offset); nil != err {
				log.Fatal(err)
			}

			videos := vp.buildSearchURLs(songs)

			for i, v := range videos {
				// 94.  Wise Up by Aimee Mann → https://www.youtube.com/results?search_query=Wise+Up+by+Aimee+Mann+music+video
				fmt.Printf(
					"%d. %s %s %s → %s \n",
					offset+i+1,
					"\033[31m", // color red
					v.Name,
					"\033[0m", // color reset
					v.SearchURL,
				)
			}

			if !hasNextPage {
				break
			}

			var choice string
			for {
				fmt.Printf("Hit enter for the next page, or type n to choose a new playlist: ")

				// Debug: rogers playlist uri -> 16FifVITGI0ud84IuTgj03
				if _, err = fmt.Scanln(&choice); nil != err && err.Error() != "unexpected newline" {
					log.Fatal(err)
				}

				if strings.TrimSpace(choice) == "" {
					pageNum++
					break
				} else if strings.TrimSpace(choice) == "n" {
					break
				} else {
					fmt.Printf("unknown choice '%s'\n", choice)
				}
			}

			if choice == "n" {
				break
			}
		}
	}
}

func (vp *videoPlaylist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var err error

	if err = r.ParseForm(); nil != err {
		log.Printf("error parsing form, err %s", err)
		log.Fatal(err)
	}

	// If there's a uri then set up the playlist data.
	// Serve the page in the case of either empty or non-empty data.
	var d tmpldata
	plURI := r.Form.Get("uri")
	if strings.TrimSpace(plURI) != "" {
		pageNumStr := r.Form.Get("page")

		if d, err = vp.buildTemplateData(plURI, pageNumStr); nil != err {
			log.Printf("error building template data, err %s", err)

			if err.Error() == errNotFound {
				d.ErrMessage = fmt.Sprintf("playlist not found by uri '%s'", plURI)
			} else {
				d.ErrMessage = errServerError
			}
		}
	}

	if plURI == "" {
		d.ErrMessage = "spotify uri is empty"
	}

	var t *template.Template
	if t, err = template.New("index").Parse(page); err != nil {
		log.Fatal(err)
	}

	t.ExecuteTemplate(w, "index", d)
}

func (vp *videoPlaylist) buildTemplateData(
	plURI string,
	pageNumStr string,
) (tmpldata, error) {
	var err error
	var d tmpldata

	// Spotify includes this prefix, but including it in
	// the search will make us 404.
	plURI = strings.TrimPrefix(plURI, uriPrefix)

	var pageNum int
	if pageNum, err = strconv.Atoi(pageNumStr); nil != err || pageNumStr == "" {
		pageNum = 1
	}

	offset := (pageNum - 1) * pageSize

	key := fmt.Sprintf("%s:%d", plURI, pageNum)
	results, ok := vp.che[key]
	if ok {
		log.Printf("cache hit with key %s", key)
	}
	if !ok {
		log.Printf("cache miss with key %s, will fetch video data", key)

		var songs []string
		var hasNextPage bool
		if songs, hasNextPage, err = vp.getPlaylistSongs(plURI, offset); nil != err {
			return d, err
		}

		videos := vp.buildSearchURLs(songs)

		results = videoPage{
			videos:  videos,
			hasNext: hasNextPage,
		}

		vp.che[key] = results
	}

	// We know there's a previous page of songs if subtracting
	// the limit is above 0. We know there's a next if the API
	// returned a Next value in the playlist API response.
	var prevURL, nextURL string
	if offset-pageSize > -1 {
		prevURL = fmt.Sprintf("/?uri=%s&page=%d", plURI, pageNum-1)
	}
	if results.hasNext {
		nextURL = fmt.Sprintf("/?uri=%s&page=%d", plURI, pageNum+1)
	}

	d = tmpldata{
		PlaylistURI: plURI,
		Videos:      results.videos,
		Previous:    prevURL,
		Next:        nextURL,
	}

	return d, nil
}

func (vp *videoPlaylist) getSpotifyToken() (string, error) {
	var err error

	log.Print("fetching spotify token")

	client := http.Client{}
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("%s/token", spotifyAuthAPIBase),
		strings.NewReader("grant_type=client_credentials"),
	)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	enc := base64.StdEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%s:%s", vp.cfg.spotifyClientID, vp.cfg.spotifyClientSecret)),
	)

	req.Header.Add("Authorization", fmt.Sprintf("Basic %s", enc))

	var res *http.Response
	if res, err = client.Do(req); nil != err {
		return "", err
	}
	defer res.Body.Close()

	var resBytes []byte
	if resBytes, err = ioutil.ReadAll(res.Body); nil != err {
		return "", err
	}

	var tokenRes = struct {
		Token string `json:"access_token"`
		Error string `json:"error"`
	}{}

	log.Printf("got spotify token %s", tokenRes.Token)

	if err = json.Unmarshal(resBytes, &tokenRes); nil != err {
		return "", err
	}

	if tokenRes.Error != "" {
		return "", fmt.Errorf("error requesting token from spotify, err %s", tokenRes.Error)
	}

	// Record the time that we successfully got this token.
	now := time.Now()
	vp.tokenSetAt = &now

	return tokenRes.Token, nil
}

func (vp *videoPlaylist) getPlaylistSongs(
	plURI string,
	offset int,
) ([]string, bool, error) {
	var err error

	if err = vp.setPlaylistReqAuth(); nil != err {
		return []string{}, false, err
	}

	log.Printf("requesting songs by playlist uri %s page %d, page size %d", plURI, offset/pageSize+1, pageSize)

	v := url.Values{}
	v.Add("fields", "items.track(name, artists),error,next,previous")
	v.Add("offset", fmt.Sprintf("%d", offset))
	v.Add("limit", fmt.Sprintf("%d", pageSize))
	encoded := v.Encode()

	client := http.Client{}

	var req *http.Request
	if req, err = http.NewRequest(
		"GET",
		fmt.Sprintf("%s/playlists/%s/tracks?%s", spotifyAPIBase, plURI, encoded),
		nil,
	); nil != err {
		return []string{}, false, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", vp.token))

	var res *http.Response
	if res, err = client.Do(req); nil != err {
		return []string{}, false, err
	}
	defer res.Body.Close()

	var resBytes []byte
	if resBytes, err = ioutil.ReadAll(res.Body); nil != err {
		return []string{}, false, err
	}

	var plres playlistRes
	if err = json.Unmarshal(resBytes, &plres); nil != err {
		return []string{}, false, err
	}

	if plres.Error.Status == http.StatusNotFound {
		return []string{}, false, errors.New(errNotFound)
	}

	// If there's a value for "Next", then there are more songs left.
	var hasNextPage bool
	if plres.Next != "" {
		hasNextPage = true
	}

	log.Println("building song data strings")

	var songs []string
	for _, item := range plres.Items {
		var artists []string
		for _, a := range item.Track.Artists {
			artists = append(artists, a.Name)
		}
		songPlusArtist := fmt.Sprintf("%s by %s", item.Track.Name, strings.Join(artists, " "))

		songs = append(songs, songPlusArtist)
	}

	return songs, hasNextPage, nil
}

func (vp *videoPlaylist) setPlaylistReqAuth() error {
	var err error
	log.Println("checking on get playlist auth token")

	// Request an oauth token if necessary.
	//
	// At the time of writing, these tokens last 60min.
	// However, should check to see if there's a way to get
	// an exact expiration from the API.
	now := time.Now()

	if nil != vp.tokenSetAt && now.Sub(*vp.tokenSetAt) < 60*time.Minute {
		tokenAge := now.Sub(*vp.tokenSetAt).Minutes()
		log.Printf("reusing %.0fmin old token, will expire in %.0fmin", tokenAge, 60-tokenAge)
		return nil
	}

	if vp.token, err = vp.getSpotifyToken(); nil != err {
		return err
	}

	return nil
}

func (vp *videoPlaylist) buildSearchURLs(songs []string) []video {
	var videos []video
	for _, song := range songs {
		videos = append(videos, video{
			Name:      song,
			SearchURL: buildVideoURL(song, vp.cfg.searchMode),
		})
	}

	return videos
}

func buildVideoURL(songName string, searchMode string) string {
	v := url.Values{}

	switch searchMode {
	case searchModeMusicVideo:
		v.Add("search_query", fmt.Sprintf("%s music video", songName))
	case searchModeLive:
		v.Add("search_query", fmt.Sprintf("%s live", songName))
	case searchModeCover:
		v.Add("search_query", fmt.Sprintf("%s cover", songName))
	case searchModeShit:
		v.Add("search_query", fmt.Sprintf("%s harry potter amv", songName))
	}

	encoded := v.Encode()

	return fmt.Sprintf("%s/results?%s", searchURLBase, encoded)
}

func firstString(s1, s2 string) string {
	if s1 == "" {
		return s2
	}
	return s1
}

const page = `
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Video Playlist Maker</title>
</head>
<body style="font-family: Courier New; padding: 45px; text-align: center">
	<div>Hi! 👋 It's good to see you.</div><br>
	<div style="max-width: 600px; margin:auto">
			<form action="/" method="POST">
				<label for="uri">Enter the URI of a public Spotify playlist and I'll generate music video youtube search urls for the songs in that playlist.</label><br><br>
				<p>Example in share link: https://open.spotify.com/playlist/<strong>4mtsECh01GM83NtxaGMfJh</strong>?si=81ec6d279895419b</p>
				<input type="text" id="uri" name="uri" style="font-size: .8em" />
				<input type="submit" value="Submit">
			</form>
			</div>
		{{ if ne "" .ErrMessage }}
			<div style="color: red; font-size: .8em; padding-top: 5px">
				{{ .ErrMessage }}
			</div>
		{{ end }}
		{{ if ne 0 (len .Videos) }} 
			<p>I hope you find some videos for songs you like that you didn't know existed :)</p>
				{{ range $v := .Videos }}
					<a href="{{ $v.SearchURL }}" target="_blank">{{ $v.Name }}</p>
				{{ end }}
		{{ end }}

		{{ if ne "" .Previous }}
			<a href="{{ .Previous }}" style="text-decoration:none">←</a> 
		{{ end }}
		
		{{ if ne "" .Next }}
			<a href="{{ .Next }}" style="text-decoration:none">→</a>
		{{ end }}
	</div>
</body>
</html>
`
