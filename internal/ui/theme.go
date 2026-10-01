// Package ui implements the bubbletea TUI of the trainer.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colorPrimary = lipgloss.Color("#61AFEF") // soft blue
	colorGreen   = lipgloss.Color("#98C379")
	colorYellow  = lipgloss.Color("#E5C07B")
	colorRed     = lipgloss.Color("#E06C75")
	colorCyan    = lipgloss.Color("#56B6C2")
	colorDim     = lipgloss.Color("#5C6370")
	colorBg      = lipgloss.Color("#282C34")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary).
			Padding(0, 2)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorDim)

	styleMenu = lipgloss.NewStyle().
			Padding(0, 2)

	styleMenuItem = lipgloss.NewStyle().
			Padding(0, 2)

	styleMenuSel = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary).
			Padding(0, 2)

	styleDone = lipgloss.NewStyle().
			Foreground(colorGreen)

	stylePending = lipgloss.NewStyle().
			Foreground(colorDim)

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim)

	styleTaskBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorYellow)

	styleTermBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorCyan)

	styleHeader = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colorDim).
			Padding(0, 1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorDim).
			Padding(0, 1)

	styleTab = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorDim)

	styleTabActive = lipgloss.NewStyle().
			Padding(0, 2).
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(colorPrimary)

	stylePrompt = lipgloss.NewStyle().
			Foreground(colorGreen)

	styleOutput = lipgloss.NewStyle()

	styleStderr = lipgloss.NewStyle().
			Foreground(colorRed)

	styleSuccess = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorGreen)

	styleHint = lipgloss.NewStyle().
			Foreground(colorYellow)

	styleCode = lipgloss.NewStyle().
			Foreground(colorCyan)

	styleBold = lipgloss.NewStyle().Bold(true)

	styleKeyword = lipgloss.NewStyle().Foreground(colorYellow)
)

// renderMarkdown renders the lightweight markup used in lesson theory:
// fenced code blocks, #-headers, bullets, `code`, **bold**.
func renderMarkdown(src string, width int) string {
	var out []string
	inCode := false
	for _, line := range strings.Split(src, "\n") {
		switch {
		case strings.HasPrefix(line, "```"):
			inCode = !inCode
			continue
		case inCode:
			out = append(out, styleCode.Render("  "+line))
		case strings.HasPrefix(line, "### "):
			out = append(out, styleBold.Render(line[4:]))
		case strings.HasPrefix(line, "## "):
			out = append(out, styleBold.Foreground(colorCyan).Render("§ "+line[3:]))
		case strings.HasPrefix(line, "# "):
			out = append(out, styleTitle.Render(line[2:]))
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			out = append(out, "  "+styleKeyword.Render("• ")+renderInline(line[2:]))
		case line == "":
			out = append(out, "")
		default:
			out = append(out, "  "+renderInline(line))
		}
	}
	return strings.Join(out, "\n")
}

// renderInline highlights `code` and **bold** spans.
func renderInline(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], "`"):
			end := strings.IndexByte(s[i+1:], '`')
			if end >= 0 {
				b.WriteString(styleCode.Render(s[i+1 : i+1+end]))
				i += end + 2
				continue
			}
			b.WriteByte(s[i])
			i++
		case strings.HasPrefix(s[i:], "**"):
			end := strings.Index(s[i+2:], "**")
			if end >= 0 {
				b.WriteString(styleBold.Render(s[i+2 : i+2+end]))
				i += end + 4
				continue
			}
			b.WriteByte(s[i])
			i++
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// wrapText wraps text to width preserving words (ANSI-aware enough for
// plain strings; styled strings are not wrapped).
func wrapText(s string, width int) []string {
	if width < 10 {
		width = 10
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(para)
		cur := ""
		for _, w := range words {
			if cur == "" {
				cur = w
			} else if len(cur)+1+len(w) <= width {
				cur += " " + w
			} else {
				lines = append(lines, cur)
				cur = w
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
