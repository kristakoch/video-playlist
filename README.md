# video-playlist

View a list of youtube search urls based on songs from a public spotify playlist to save time thinking and typing up search terms.

### Running the project
The project can be run with `go run main.go` from the project root.
1. Create a new application in the spotify developer dashboard. Instructions here: https://developer.spotify.com/documentation/general/guides/app-settings/
2. Add your app client id and secret as env vars or provide them using flags when you run the program.
3. Run the code with `go run main.go` in the root of the project directory.
4. Visit the site running at http://localhost:1313.
5. Add the URI of a spotify playlist into the text input and submit the form to generate a list of search URLs.

### Running the project with realize

You could alternatively use `realize` to run the server. This will give you live reloading when you change the code.

1. Same as step 1 above.
2. Run `go get github.com/oexequa/realize`.
3. Rename the `.realize-example.yaml` file to `realize.yaml`. This file will contain your credentials, so it's included in the `.gitignore`.
4. Add your spotify client id and secret to the `realize` file.
5. Run the code with `realize start` in the root of the project directory. 
6. Same as steps 4 and 5 above.

