package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config holds user settings read from config.json in the data directory.
// A missing file is not an error: every setting is optional.
type Config struct {
	// JiraURL is the base of the Jira site, e.g. https://example.atlassian.net.
	// Ticket links go to JiraURL + "/browse/" + key; empty disables them.
	JiraURL string `json:"jiraUrl"`
}

func loadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("parsing %s: %w", path, err)
	}
	c.JiraURL = strings.TrimRight(c.JiraURL, "/")

	return c, nil
}

