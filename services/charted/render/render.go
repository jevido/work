// Package render turns a page's markdown into what Charted stores: HTML to
// serve, plain text to search, a heading list for the contents, and every
// internal link the page makes.
//
// All four come out of one parse. Rendering twice -- once for HTML and once to
// walk for links -- is the obvious way to write this and the way the two drift:
// a link that the renderer rewrote and the walker did not is a link check that
// passes on a page that is broken.
package render

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Heading is one entry in a page's contents.
type Heading struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
}

// Link is one internal link, already resolved to the page it points at.
type Link struct {
	Space string `json:"space"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

// Page is everything one markdown document becomes.
type Page struct {
	HTML     string
	Plain    string
	Headings []Heading
	Links    []Link
}

// The renderer, built once. goldmark's Markdown is safe for concurrent use and
// building one per request would recompile the Chroma styles on every write.
//
// Unsafe HTML stays off. A docs page is written by an agent, which is to say by
// a model reading somebody's repository -- the one kind of author who can be
// talked into emitting a <script> by the contents of a file it was asked to
// summarise. Nothing here needs raw HTML badly enough to pay for that.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		highlighting.NewHighlighting(
			// A class-based formatter rather than inline styles: the reader
			// has a dark mode, and a <span style="color:#abc"> baked in at
			// write time cannot follow it. The classes are styled in the
			// reader's CSS, twice, once per theme.
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		),
	),
	goldmark.WithParserOptions(
		// Heading ids, which the contents list links to and the reader scrolls
		// by. Generated from the heading text, so they survive a re-render.
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
	),
)

var tagRE = regexp.MustCompile(`<[^>]*>`)
var spaceRE = regexp.MustCompile(`\s+`)

// Render parses one page. space is the space the page itself lives in, which is
// what a relative link resolves against.
func Render(space, markdown string) (Page, error) {
	src := []byte(markdown)
	doc := md.Parser().Parse(text.NewReader(src))

	var out bytes.Buffer
	if err := md.Renderer().Render(&out, src, doc); err != nil {
		return Page{}, fmt.Errorf("render: %w", err)
	}

	page := Page{HTML: out.String()}

	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			id, ok := node.AttributeString("id")
			if !ok {
				return ast.WalkContinue, nil
			}
			page.Headings = append(page.Headings, Heading{
				ID:    string(util.EscapeHTML(id.([]byte))),
				Text:  string(node.Text(src)),
				Level: node.Level,
			})
		case *ast.Link:
			target := string(node.Destination)
			if to, ok := resolve(space, target); ok {
				to.Label = string(node.Text(src))
				page.Links = append(page.Links, to)
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return Page{}, fmt.Errorf("walk: %w", err)
	}

	page.Plain = plain(page.HTML)
	return page, nil
}

// resolve says whether a link destination points at another Charted page, and
// at which one.
//
// Three shapes are internal, and everything else -- a scheme, a protocol-
// relative "//host", a bare fragment -- is somebody else's URL and is left
// alone:
//
//	/space/slug     an absolute page, anywhere in the site
//	slug            a page in the same space
//	./slug          the same, written the way a file would be
func resolve(space, dest string) (Link, bool) {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "//") {
		return Link{}, false
	}
	if strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
		return Link{}, false
	}
	// A fragment on an internal link points inside the page it names, so the
	// page is what gets checked.
	if at := strings.IndexByte(dest, '#'); at >= 0 {
		dest = dest[:at]
	}
	dest = strings.TrimSuffix(dest, "/")
	if dest == "" {
		return Link{}, false
	}
	dest = strings.TrimPrefix(dest, "./")

	if strings.HasPrefix(dest, "/") {
		parts := strings.SplitN(strings.TrimPrefix(dest, "/"), "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return Link{}, false
		}
		return Link{Space: parts[0], Slug: parts[1]}, true
	}
	return Link{Space: space, Slug: dest}, true
}

// plain is the rendered page with its tags taken out, which is what search
// indexes and excerpts from.
//
// Stripping the HTML rather than walking the AST for text: the HTML is what a
// reader sees, so this is what they searched for. It also means a code block's
// contents are searchable, which is most of what anybody looks for in
// documentation.
func plain(rendered string) string {
	s := tagRE.ReplaceAllString(rendered, " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}
