// Package patroni reads cluster state from the Patroni REST API.
package patroni

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Member is one node as Patroni lists it.
type Member struct {
	Name     string
	Role     string // leader, replica, sync_standby, standby_leader
	State    string // running, streaming, stopped, start failed, and so on
	Host     string
	Port     int
	Timeline int
	// LagBytes is the replay lag in bytes, or -1 when Patroni reports it as unknown or omits it.
	LagBytes int64
}

// Cluster is the answer of GET /cluster.
type Cluster struct {
	Scope   string
	Paused  bool
	Members []Member
}

// maxBody bounds the response, because the URL comes from the command line and may not be Patroni.
const maxBody = 1 << 20

// BaseURL checks and cleans a user-supplied URL. It accepts http and https,
// drops any path, query and credentials, and returns scheme://host[:port].
func BaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid Patroni URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("the Patroni URL must look like http://host:8008")
	}
	return u.Scheme + "://" + u.Host, nil
}

// Fetch reads /cluster from the Patroni REST API at base.
// src: https://patroni.readthedocs.io/en/latest/rest_api.html#cluster-status-endpoints
func Fetch(ctx context.Context, base string) (*Cluster, error) {
	base, err := BaseURL(base)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/cluster", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /cluster returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return parse(body)
}

// parse decodes the /cluster JSON. It rejects a body with no members, so a
// server that is not Patroni does not pass as one.
func parse(body []byte) (*Cluster, error) {
	var raw struct {
		Scope   string `json:"scope"`
		Pause   bool   `json:"pause"`
		Members []struct {
			Name     string          `json:"name"`
			Role     string          `json:"role"`
			State    string          `json:"state"`
			Host     string          `json:"host"`
			Port     int             `json:"port"`
			Timeline int             `json:"timeline"`
			Lag      json.RawMessage `json:"lag"`
		} `json:"members"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("not a Patroni /cluster answer: %w", err)
	}
	if len(raw.Members) == 0 {
		return nil, errors.New("not a Patroni /cluster answer: no members")
	}
	c := &Cluster{Scope: raw.Scope, Paused: raw.Pause}
	for _, m := range raw.Members {
		mem := Member{Name: m.Name, Role: m.Role, State: m.State, Host: m.Host, Port: m.Port, Timeline: m.Timeline, LagBytes: -1}
		var lag int64
		if len(m.Lag) > 0 && json.Unmarshal(m.Lag, &lag) == nil {
			mem.LagBytes = lag
		}
		c.Members = append(c.Members, mem)
	}
	return c, nil
}
