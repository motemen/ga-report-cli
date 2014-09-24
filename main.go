package main

// https://developers.google.com/analytics/devguides/reporting/core/v3/

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"text/template"

	"code.google.com/p/goauth2/oauth"
	"code.google.com/p/google-api-go-client/analytics/v3"
)

type clientSecret struct {
	Web struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	} `json:"web"`
}

var rootDirectory string

func init() {
	usr, err := user.Current()
	if err != nil {
		panic(err)
	}

	rootDirectory = filepath.Join(usr.HomeDir, ".ga-report-cli")
}

func fixupWithGaPrefix(namesString string) string {
	names := strings.Split(namesString, ",")
	result := make([]string, len(names))

	for i, name := range names {
		if strings.Contains(name, ":") == false {
			name = "ga:" + name
		}
		result[i] = name
	}

	return strings.Join(result, ",")
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)

	var (
		forceAuth  = flagSet.Bool("force-auth", false, "force authorization")
		dimensions = flagSet.String("dimension", "", "report dimensions (separated by comma)")
		metrics    = flagSet.String("metrics", "", "report metrics (separated by comma)")
		profileID  = flagSet.String("profile", "", "Google Analytics profile ID")
		format     = flagSet.String("format", "", "output format")
	)

	flagSet.Parse(os.Args[1:])

	config, err := loadOAuthConfig()
	if err != nil {
		log.Fatal(err)
	}

	client := prepareOAuthClient(config, *forceAuth)

	analyticsService, err := analytics.New(client)
	// accounts, err := analyticsService.Management.Accounts.List().Do()
	gaData, err := analyticsService.Data.Ga.Get(
		"ga:"+*profileID,
		"today",
		"today",
		fixupWithGaPrefix(*metrics),
	).Dimensions(
		fixupWithGaPrefix(*dimensions),
	).Do()

	if err != nil {
		log.Fatal(err)
	}

	w := &tabwriter.Writer{}
	w.Init(os.Stdout, 0, 8, 1, '\t', 0)

	headers := []string{}
	for _, header := range gaData.ColumnHeaders {
		headers = append(headers, strings.TrimPrefix(header.Name, "ga:"))
	}
	if *format == "" {
		fmt.Fprintln(w, strings.Join(headers, "\t"))
	}

	for _, row := range gaData.Rows {
		entry := map[string]string{}
		for i, header := range headers {
			entry[header] = row[i]
		}

		if *format == "" {
			fmt.Fprintln(w, strings.Join(row, "\t"))
		} else {
			tmpl := template.Must(template.New("format").Parse(*format + "\n"))
			tmpl.Execute(w, entry)
		}
	}
	w.Flush()
}

func obtainToken(config *oauth.Config) *oauth.Token {
	ch := make(chan string)
	ts := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/favicon.ico" {
				http.Error(w, "Not Found", 404)
				return
			}

			if code := req.FormValue("code"); code != "" {
				w.Header().Set("Content-Type", "text/plain")
				fmt.Fprintln(w, "Authorized.")
				ch <- code
				return
			}

			http.Error(w, "Internal Server Error", 500)
			log.Fatalf("Could not handle request: %+v", req)
		}))
	defer ts.Close()

	config.RedirectURL = ts.URL

	authURL := config.AuthCodeURL("")

	log.Printf("Visit %s to authorize", authURL)
	exec.Command("open", authURL).Run()

	code := <-ch

	t := &oauth.Transport{Config: config}
	token, err := t.Exchange(code)
	if err != nil {
		log.Fatal(err)
	}

	return token
}

func loadOAuthConfig() (*oauth.Config, error) {
	var cs clientSecret
	err := loadJSONFromFile(filepath.Join(rootDirectory, "client_secret.json"), &cs)
	if err != nil {
		return nil, fmt.Errorf("%s; obtain one at <https://console.developers.google.com/project>", err)
	}

	config := &oauth.Config{
		ClientId:     cs.Web.ClientID,
		ClientSecret: cs.Web.ClientSecret,
		Scope:        analytics.AnalyticsReadonlyScope,
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://accounts.google.com/o/oauth2/token",
		AccessType:   "offline",
		TokenCache:   oauth.CacheFile(filepath.Join(rootDirectory, "auth_cache.json")),
	}

	return config, nil
}

func prepareOAuthClient(config *oauth.Config, useFresh bool) *http.Client {
	token := &oauth.Token{}

	if useFresh == false {
		err := loadJSONFromFile(filepath.Join(rootDirectory, "auth_cache.json"), token)
		if err != nil {
			useFresh = true
		}
	}

	if useFresh {
		config.ApprovalPrompt = "force"
		token = obtainToken(config)
	}

	t := &oauth.Transport{
		Token:  token,
		Config: config,
	}
	return t.Client()
}

func loadJSONFromFile(path string, v interface{}) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}

	defer r.Close()

	return json.NewDecoder(r).Decode(v)
}
