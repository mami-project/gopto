package pto3

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/go-pg/pg"
)

// PTOServerConfig contains a configuration of a PTO server
type PTOServerConfig struct {
	// Address/port to bind to
	BindTo string

	// base URL of web service
	BaseURL string
	// ...this right here is effing annoying but i'm not writing a custom unmarshaler just for that...
	baseURL *url.URL

	// API key filename
	APIKeyFile string

	// base path for raw data store; empty for no RDS.
	RawRoot string

	// Filetype registry for RDS.
	ContentTypes map[string]string

	// base path for query cache data store; empty for no query cache.
	//QueryCacheRoot string

	// PostgreSQL options for connection to observation database; leave default for no OBS.
	ObsDatabase pg.Options
}

func (config *PTOServerConfig) ParseURL() error {
	var err error
	config.baseURL, err = url.Parse(config.BaseURL)
	return err
}

func (config *PTOServerConfig) HandleRoot(w http.ResponseWriter, r *http.Request) {

	links := make(map[string]string)

	if config.RawRoot != "" {
		rawrel, _ := url.Parse("raw")
		links["raw"] = config.baseURL.ResolveReference(rawrel).String()
	}

	if config.ObsDatabase.Database != "" {
		obsrel, _ := url.Parse("obs")
		links["obs"] = config.baseURL.ResolveReference(obsrel).String()
	}

	linksj, err := json.Marshal(links)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(linksj)
}

func LoadConfig(filename string) (*PTOServerConfig, error) {
	var config PTOServerConfig

	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if err = json.Unmarshal(b, &config); err != nil {
		return nil, err
	}

	if err = config.ParseURL(); err != nil {
		return nil, err
	}

	return &config, nil
}
