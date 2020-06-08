# video-playlist

App that makes a list of youtube search urls based on songs from a public spotify playlist.

Run with `go run main.go` and visit the site running at http://localhost:1313. You'll need to add your spotify key into your env.

You could also use the included example `realize` file to run the server. 

To do that, you'll need to do a `go get github.com/oxequa/realize`, change the file name to `.realize.yaml`, and use `realize start` to get live reloading. Be sure to add in your spotify key to the realize file or env. Find out how to register your app by going [here](https://developer.spotify.com/documentation/general/guides/app-settings/#register-your-app).

You'll need to provide the key in the format: 
`Authorization: Basic <base64 encoded client_id:client_secret>`

