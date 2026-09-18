// Package charted publishes pages to a Charted site.
//
// It is the desktop app's half of documentation mode: the agent writes a page
// onto the board, a person reads it and presses Publish, and this is what
// crosses the network. Deliberately the only place in the app that talks to
// Charted, and deliberately not reachable from the MCP server -- a tool the
// model can call on its own is a tool that publishes without anybody reading
// what it wrote.
package charted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNoSite is what every call returns when this machine has no Charted
// configured. Named, so the UI can say "set one up" rather than "request
// failed".
var ErrNoSite = errors.New("charted: no documentation site is configured")

// ErrNoToken is a site this machine can read but not write.
var ErrNoToken = errors.New("charted: no write token for this documentation site")

// Link is one internal link that points at a page which does not exist yet.
type Link struct {
	Space string `json:"space"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

// Page is what a publish sends.
type Page struct {
	Space       string `json:"-"`
	Slug        string `json:"-"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Markdown    string `json:"markdown"`
	Position    int    `json:"position,omitempty"`
}

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// New builds a client for one site. baseURL is the origin; an empty one is
// [ErrNoSite], because "not configured" is the common case and is not an error
// worth a different shape.
func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, ErrNoSite
	}
	parsed, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("charted: %q is not a site address", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("charted: %q has to start with http:// or https://", baseURL)
	}
	return &Client{
		base:  parsed,
		token: strings.TrimSpace(token),
		// A publish is one small request to a site that is either up or not.
		// Thirty seconds is long enough for a slow link and short enough that
		// a wrong address is an error rather than a spinner.
		http: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Writable says whether this client could publish, without trying.
func (c *Client) Writable() bool { return c != nil && c.token != "" }

// Publish writes one page and returns the links on it that point nowhere.
//
// Broken links come back rather than failing the call, exactly as the server
// reports them: a page that links ahead to one not written yet is normal, and
// refusing it would mean writing documentation from the leaves inwards.
func (c *Client) Publish(ctx context.Context, page Page) ([]Link, error) {
	if c == nil {
		return nil, ErrNoSite
	}
	if c.token == "" {
		return nil, ErrNoToken
	}
	if page.Space == "" || page.Slug == "" || page.Title == "" {
		return nil, errors.New("charted: a page needs a space, a slug and a title")
	}

	body, err := json.Marshal(page)
	if err != nil {
		return nil, fmt.Errorf("charted: encoding the page: %w", err)
	}

	var answer struct {
		Broken []Link `json:"broken"`
	}
	if err := c.do(ctx, http.MethodPut, c.pagePath(page.Space, page.Slug), body, &answer); err != nil {
		return nil, err
	}
	return answer.Broken, nil
}

// Unpublish removes a page. A page that is already gone is not an error: the
// caller wanted it gone.
func (c *Client) Unpublish(ctx context.Context, space, slug string) error {
	if c == nil {
		return ErrNoSite
	}
	if c.token == "" {
		return ErrNoToken
	}
	err := c.do(ctx, http.MethodDelete, c.pagePath(space, slug), nil, nil)
	var status *StatusError
	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// Broken is every link on the whole site with no page behind it.
type Broken struct {
	Space   string `json:"space"`
	Slug    string `json:"slug"`
	ToSpace string `json:"toSpace"`
	ToSlug  string `json:"toSlug"`
	Label   string `json:"label"`
}

// BrokenLinks asks the site for its whole link check.
func (c *Client) BrokenLinks(ctx context.Context) ([]Broken, error) {
	if c == nil {
		return nil, ErrNoSite
	}
	if c.token == "" {
		return nil, ErrNoToken
	}
	var answer struct {
		Broken []Broken `json:"broken"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/links/broken", nil, &answer); err != nil {
		return nil, err
	}
	return answer.Broken, nil
}

// StatusError is a refusal from the site, with whatever it said about it.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("charted: the site answered %d", e.Status)
	}
	return "charted: " + e.Message
}

func (c *Client) pagePath(space, slug string) string {
	parts := make([]string, 0, 4)
	for _, part := range strings.Split(strings.Trim(slug, "/"), "/") {
		parts = append(parts, url.PathEscape(part))
	}
	return "/v1/pages/" + url.PathEscape(space) + "/" + strings.Join(parts, "/")
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, into any) error {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, reader)
	if err != nil {
		return fmt.Errorf("charted: building the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("charted: reaching %s: %w", c.base.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		// The site's own message when it sent one. Its errors are written for
		// a person -- "not a writer", "this deployment has no write token" --
		// and repeating them beats inventing a worse sentence here.
		var said struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&said)
		return &StatusError{Status: resp.StatusCode, Message: said.Message}
	}
	if into == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("charted: reading the answer: %w", err)
	}
	return nil
}
