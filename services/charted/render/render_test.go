package render

import (
	"strings"
	"testing"
)

func TestRenderProducesHTMLAndText(t *testing.T) {
	page, err := Render("guides", "# Title\n\nSome **prose** with `code` in it.\n")
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !strings.Contains(page.HTML, "<strong>prose</strong>") {
		t.Errorf("html has no rendered emphasis in it:\n%s", page.HTML)
	}
	if strings.Contains(page.Plain, "<") {
		t.Errorf("plain text still has markup in it: %q", page.Plain)
	}
	if !strings.Contains(page.Plain, "Some prose with code in it.") {
		t.Errorf("plain text = %q, want the sentence in it", page.Plain)
	}
}

// Raw HTML in a page must not reach the reader. The author of a page is an
// agent summarising somebody's repository, so "the markdown contained a script
// tag" is a thing that happens by accident before it happens on purpose.
func TestRenderDropsRawHTML(t *testing.T) {
	page, err := Render("guides", "Before\n\n<script>alert(1)</script>\n\nAfter\n")
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if strings.Contains(page.HTML, "<script>") {
		t.Errorf("a script tag survived into the html:\n%s", page.HTML)
	}
}

func TestRenderCollectsHeadings(t *testing.T) {
	page, err := Render("guides", "# Page\n\n## First thing\n\ntext\n\n### Under it\n\nmore\n")
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if len(page.Headings) != 3 {
		t.Fatalf("headings = %d, want 3: %+v", len(page.Headings), page.Headings)
	}
	if page.Headings[1].Text != "First thing" || page.Headings[1].Level != 2 {
		t.Errorf("second heading = %+v, want the h2", page.Headings[1])
	}
	if page.Headings[1].ID == "" {
		t.Error("a heading with no id cannot be linked to from the contents")
	}
}

func TestRenderResolvesInternalLinks(t *testing.T) {
	markdown := strings.Join([]string{
		"[absolute](/other/page)",
		"[same space](sibling)",
		"[dotted](./dotted-one)",
		"[fragment of another](/other/page#part)",
		"[external](https://example.com/page)",
		"[mail](mailto:someone@example.com)",
		"[anchor](#heading)",
	}, "\n\n")

	page, err := Render("guides", markdown)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}

	want := []Link{
		{Space: "other", Slug: "page", Label: "absolute"},
		{Space: "guides", Slug: "sibling", Label: "same space"},
		{Space: "guides", Slug: "dotted-one", Label: "dotted"},
		{Space: "other", Slug: "page", Label: "fragment of another"},
	}
	if len(page.Links) != len(want) {
		t.Fatalf("links = %+v, want %d of them", page.Links, len(want))
	}
	for at, link := range want {
		if page.Links[at] != link {
			t.Errorf("link %d = %+v, want %+v", at, page.Links[at], link)
		}
	}
}

func TestRenderHighlightsCodeWithClasses(t *testing.T) {
	page, err := Render("guides", "```go\nfunc main() {}\n```\n")
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	// Classes, not inline styles: the reader has two themes and a colour baked
	// in at write time cannot follow the one being read.
	if strings.Contains(page.HTML, "style=\"color:") {
		t.Errorf("code was highlighted with inline styles:\n%s", page.HTML)
	}
	if !strings.Contains(page.HTML, "chroma") {
		t.Errorf("code was not highlighted at all:\n%s", page.HTML)
	}
}
