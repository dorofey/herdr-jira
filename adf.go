package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type adfNode struct {
	Type, Text string
	Attrs      map[string]any
	Content    []adfNode
	Marks      []struct {
		Type  string
		Attrs map[string]any
	}
}

func adfMarkdown(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var n adfNode
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	return renderADF(n)
}
func renderADF(n adfNode) string {
	var b strings.Builder
	for _, c := range n.Content {
		b.WriteString(renderADF(c))
	}
	s := b.String()
	switch n.Type {
	case "text":
		s = n.Text
		for _, m := range n.Marks {
			switch m.Type {
			case "strong":
				s = "**" + s + "**"
			case "em":
				s = "*" + s + "*"
			case "code":
				s = "`" + s + "`"
			case "strike":
				s = "~~" + s + "~~"
			case "link":
				if href, ok := m.Attrs["href"].(string); ok {
					s = "[" + s + "](" + href + ")"
				}
			}
		}
		return s
	case "paragraph":
		return s + "\n\n"
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
			level = int(v)
		}
		return strings.Repeat("#", level) + " " + s + "\n\n"
	case "hardBreak":
		return "\n"
	case "rule":
		return "\n---\n"
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		return "\n```" + lang + "\n" + s + "\n```\n\n"
	case "blockquote":
		return "> " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n> ") + "\n\n"
	case "bulletList", "orderedList":
		var out strings.Builder
		for i, c := range n.Content {
			prefix := "- "
			if n.Type == "orderedList" {
				prefix = fmt.Sprintf("%d. ", i+1)
			}
			out.WriteString(prefix + strings.ReplaceAll(strings.TrimSpace(renderADF(c)), "\n", "\n  ") + "\n")
		}
		return out.String() + "\n"
	case "mention":
		v, _ := n.Attrs["text"].(string)
		return v
	case "emoji":
		v, _ := n.Attrs["text"].(string)
		if v == "" {
			v, _ = n.Attrs["shortName"].(string)
		}
		return v
	case "inlineCard", "blockCard":
		v, _ := n.Attrs["url"].(string)
		return v
	case "media", "mediaSingle", "mediaGroup":
		return "[Attachment: open in Jira]\n"
	case "tableRow":
		return s + "\n"
	case "tableCell", "tableHeader":
		return strings.TrimSpace(s) + " | "
	}
	return s
}
func plainADF(text string) map[string]any {
	var nodes []any
	for _, line := range strings.Split(text, "\n") {
		p := map[string]any{"type": "paragraph"}
		if line != "" {
			p["content"] = []any{map[string]string{"type": "text", "text": line}}
		}
		nodes = append(nodes, p)
	}
	return map[string]any{"type": "doc", "version": 1, "content": nodes}
}
