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

const limit = 100

const (
	errServerError = "server error"
	errNotFound    = "not found"
)

const (
	uriPrefix          = "spotify:playlist:"
	searchURLBase      = "https://www.youtube.com"
	spotifyAPIBase     = "https://api.spotify.com/v1"
	spotifyAuthAPIBase = "https://accounts.spotify.com/api"

	modeMusicVideo = "music video"
	modeLive       = "live"
	modeCover      = "cover"
	modeShit       = "shit"
)

type config struct {
	spotifyClientID     string
	spotifyClientSecret string
	mode                string
}

type handler struct {
	spotifyClientID     string
	spotifyClientSecret string
	token               string
	mode                string
	tokenSetAt          *time.Time
	che                 map[string]videoPage
}

type tmpldata struct {
	PlaylistURI string
	Videos      []video
	ErrMessage  string
	Previous    string
	Next        string
}

type videoPage struct {
	videos     []video
	isNextPage bool
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

	flag.StringVar(&cfg.spotifyClientID, "spotifyClientID", "", "spotify client id")
	flag.StringVar(&cfg.spotifyClientSecret, "spotifyClientSecret", "", "spotify client secret")
	flag.StringVar(&cfg.mode, "mode", modeMusicVideo, "search mode (default is music video)")
	flag.Parse()

	cfg.spotifyClientID = firstString(os.Getenv("SPOTIFY_CLIENT_ID"), cfg.spotifyClientID)
	cfg.spotifyClientSecret = firstString(os.Getenv("SPOTIFY_CLIENT_SECRET"), cfg.spotifyClientSecret)

	if cfg.spotifyClientID == "" {
		log.Fatal("spotify client id is empty")
	}
	if cfg.spotifyClientSecret == "" {
		log.Fatal("spotify client secret empty")
	}

	log.Printf(`
		video playlister running with
		client id:     %s
		client secret: %s
		mode:          %s 
	`,
		cfg.spotifyClientID,
		cfg.spotifyClientSecret,
		cfg.mode,
	)

	cache := make(map[string]videoPage)

	h := handler{
		spotifyClientID:     cfg.spotifyClientID,
		spotifyClientSecret: cfg.spotifyClientSecret,
		che:                 cache,
		mode:                cfg.mode,
	}

	r := mux.NewRouter()
	r.Handle("/", &h).Methods("GET", "POST")

	log.Printf("🧜‍♂️  server is running at http://localhost:%d", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), r))
}

func (c *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

		if d, err = c.buildTemplateData(plURI, pageNumStr); nil != err {
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

func (c *handler) buildTemplateData(
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

	offset := (pageNum - 1) * limit

	// Request song data, creating a group of song+artist strings.
	// Before requestins, check to see if we have this uri:page cached.
	key := fmt.Sprintf("%s:%d", plURI, pageNum)
	results, ok := c.che[key]
	if ok {
		log.Printf("cache hit with key %s", key)
	}
	if !ok {
		log.Printf("cache miss with key %s, will fetch video data", key)

		// Request an oauth token.
		// Before requesting, check to see we already have an unexpired token.
		now := time.Now()
		if c.tokenSetAt == nil || now.Sub(*c.tokenSetAt) > 60*time.Minute {
			if c.token, err = c.getSpotifyToken(); nil != err {
				return d, err
			}
		} else {
			tokenAge := now.Sub(*c.tokenSetAt).Minutes()
			log.Printf("reusing %.0fmin old token, will expire in %.0fmin", tokenAge, 60-tokenAge)
		}

		var songs []string
		var isNextPage bool
		if songs, isNextPage, err = c.getPlaylistSongs(plURI, offset); nil != err {
			return d, err
		}

		// Build the youtube search urls.
		videos := c.buildVideos(songs)
		results = videoPage{
			videos:     videos,
			isNextPage: isNextPage,
		}

		c.che[key] = results
	}

	// We know there's a previous page of songs if sutracting
	// the limit is above 0. We know there's a next if the API
	// returned a Next value in the playlist API response.
	var prevURL, nextURL string
	if offset-limit > -1 {
		prevURL = fmt.Sprintf("/?uri=%s&page=%d", plURI, pageNum-1)
	}
	if results.isNextPage {
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

func (c *handler) getSpotifyToken() (string, error) {
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
		[]byte(fmt.Sprintf("%s:%s", c.spotifyClientID, c.spotifyClientSecret)),
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
	c.tokenSetAt = &now

	return tokenRes.Token, nil
}

func (c *handler) getPlaylistSongs(
	plURI string,
	offset int,
) ([]string, bool, error) {
	var err error

	log.Printf("requesting songs by playlist uri %s page %d, page size %d", plURI, offset/limit+1, limit)

	v := url.Values{}
	v.Add("fields", "items.track(name, artists),error,next,previous")
	v.Add("offset", fmt.Sprintf("%d", offset))
	v.Add("limit", fmt.Sprintf("%d", limit))
	encoded := v.Encode()

	client := http.Client{}
	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/playlists/%s/tracks?%s", spotifyAPIBase, plURI, encoded),
		nil,
	)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))

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

func (c *handler) buildVideos(songs []string) []video {
	var videos []video
	for _, song := range songs {
		videos = append(videos, video{
			Name:      song,
			SearchURL: buildVideoURL(song, c.mode),
		})
	}

	return videos
}

func buildVideoURL(songName string, mode string) string {
	v := url.Values{}

	switch mode {
	case modeMusicVideo:
		v.Add("search_query", fmt.Sprintf("%s music video", songName))
	case modeLive:
		v.Add("search_query", fmt.Sprintf("%s live", songName))
	case modeCover:
		v.Add("search_query", fmt.Sprintf("%s cover", songName))
	case modeShit:
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
				<label for="uri">Enter the URI of a public Spotify playlist and I'll generate music video search urls for the songs in that playlist:</label><br><br>
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
