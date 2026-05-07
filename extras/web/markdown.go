package web

import (
	"io"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// htmlToMarkdown turns an HTML document into a compact markdown-flavored
// text suitable for an LLM tool result. The goal is "what a reader
// would see if they opened the page" minus chrome — not a faithful
// round-trippable conversion. Heuristics:
//
//   - <script>, <style>, <noscript>, <nav>, <header>, <footer>,
//     <aside>, <form>, <button>, <input>, <select>, <textarea>,
//     <svg>, <iframe>: dropped wholesale (page chrome / interactive
//     widgets aren't useful to a reader).
//   - <h1>..<h6>: rendered as "# ".."###### ".
//   - <p>, <br>, <li>, <tr>: line breaks at the right granularity.
//   - <pre>, <code>: fenced code blocks for <pre>, backticks for inline.
//   - <a href>: rendered as "[text](href)" with hrefs resolved against
//     the base URL when one is provided.
//   - <img alt src>: "![alt](src)"; binary content is not fetched.
//   - <table>: flattened to row-per-line cells separated by " | ".
//   - everything else: text content concatenated, whitespace collapsed.
//
// We do not try to be a real markdown renderer; the output exists to be
// consumed by an LLM, not re-rendered.
func htmlToMarkdown(r io.Reader, baseURL string) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}
	var base *url.URL
	if baseURL != "" {
		if u, err := url.Parse(baseURL); err == nil {
			base = u
		}
	}
	var b strings.Builder
	walk(doc, &b, base)
	return collapseBlankLines(b.String()), nil
}

// dropTags lists tags whose entire subtree we skip. Lowercased.
var dropTags = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"nav": true, "header": true, "footer": true, "aside": true,
	"form": true, "button": true, "input": true, "select": true,
	"textarea": true, "svg": true, "iframe": true,
}

// blockTags emit a newline before/after their content.
var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"blockquote": true, "ul": true, "ol": true, "table": true,
}

func walk(n *html.Node, b *strings.Builder, base *url.URL) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		writeText(b, n.Data)
	case html.ElementNode:
		tag := n.Data
		if dropTags[tag] {
			return
		}
		switch tag {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			level := int(tag[1] - '0')
			b.WriteString("\n\n" + strings.Repeat("#", level) + " ")
			walkChildren(n, b, base)
			b.WriteString("\n\n")
			return
		case "br":
			b.WriteString("\n")
			return
		case "li":
			b.WriteString("\n- ")
			walkChildren(n, b, base)
			return
		case "tr":
			walkChildren(n, b, base)
			b.WriteString("\n")
			return
		case "td", "th":
			walkChildren(n, b, base)
			b.WriteString(" | ")
			return
		case "pre":
			b.WriteString("\n\n```\n")
			walkChildren(n, b, base)
			b.WriteString("\n```\n\n")
			return
		case "code":
			// Don't double-fence code that's already inside <pre>.
			if !inside(n, "pre") {
				b.WriteString("`")
				walkChildren(n, b, base)
				b.WriteString("`")
				return
			}
		case "a":
			href := attr(n, "href")
			if base != nil && href != "" {
				if ref, err := base.Parse(href); err == nil {
					href = ref.String()
				}
			}
			start := b.Len()
			walkChildren(n, b, base)
			text := strings.TrimSpace(b.String()[start:])
			if href != "" && text != "" {
				// Rewrite the just-emitted text as a markdown link.
				rewritten := b.String()[:start] + "[" + text + "](" + href + ")"
				b.Reset()
				b.WriteString(rewritten)
			}
			return
		case "img":
			alt := attr(n, "alt")
			src := attr(n, "src")
			if base != nil && src != "" {
				if ref, err := base.Parse(src); err == nil {
					src = ref.String()
				}
			}
			if src != "" {
				b.WriteString("![" + alt + "](" + src + ")")
			}
			return
		}
		if blockTags[tag] {
			b.WriteString("\n\n")
		}
		walkChildren(n, b, base)
		if blockTags[tag] {
			b.WriteString("\n\n")
		}
	default:
		walkChildren(n, b, base)
	}
}

func walkChildren(n *html.Node, b *strings.Builder, base *url.URL) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, b, base)
	}
}

func writeText(b *strings.Builder, s string) {
	// Collapse runs of whitespace to a single space; keep newlines from
	// block elements that the walker emits explicitly.
	prevSpace := b.Len() == 0 || isWhitespaceTail(b.String())
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
}

func isWhitespaceTail(s string) bool {
	if s == "" {
		return true
	}
	r := s[len(s)-1]
	return r == ' ' || r == '\n' || r == '\t'
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func inside(n *html.Node, tag string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == tag {
			return true
		}
	}
	return false
}

// collapseBlankLines compresses runs of 3+ newlines to exactly 2,
// matching how markdown readers usually handle paragraph breaks.
func collapseBlankLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	newlines := 0
	for _, r := range s {
		if r == '\n' {
			newlines++
			if newlines <= 2 {
				b.WriteRune(r)
			}
			continue
		}
		newlines = 0
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
