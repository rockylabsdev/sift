package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type rgb struct{ r, g, b float64 }

func hex(s string) rgb {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return rgb{float64(r), float64(g), float64(b)}
}

func (c rgb) String() string { return fmt.Sprintf("#%02x%02x%02x", int(c.r), int(c.g), int(c.b)) }

// Theme gives every colour one job. Ticket hues mark only ticket labels and
// their marks; amber marks meetings and attention, always beside words;
// borders are neutral and only the focused panel's is emphasised.
type Theme struct {
	Name                 string
	bg, text, muted, dim rgb
	faint, border, focus rgb
	sel, amber           rgb
	hue                  []rgb
}

// Themes are the built-in themes. The first is the default.
var Themes = []Theme{
	{"graphite", hex("#181C1B"), hex("#E4DFD3"), hex("#B4B0A5"), hex("#878B84"),
		hex("#343A38"), hex("#3A413E"), hex("#8C978E"), hex("#232B28"), hex("#D4B078"),
		[]rgb{hex("#B5C99B"), hex("#8FBDC1"), hex("#B5A5CC"), hex("#D9A6A0"), hex("#A8C7A4"), hex("#C9B98F")}},
	{"dusk", hex("#1B1C25"), hex("#E1E1EA"), hex("#B0B1C2"), hex("#878999"),
		hex("#33354A"), hex("#383A4C"), hex("#868AA2"), hex("#272939"), hex("#D8B682"),
		[]rgb{hex("#AABCE0"), hex("#91BEB3"), hex("#C3A6C9"), hex("#E0A9A9"), hex("#B6C99A"), hex("#9FB3C8")}},
	{"paper", hex("#F2EFE7"), hex("#2B2A26"), hex("#55534C"), hex("#77746A"),
		hex("#D6D2C6"), hex("#C9C5B8"), hex("#8C887B"), hex("#E3E5D8"), hex("#866119"),
		[]rgb{hex("#526D3F"), hex("#306C74"), hex("#765A89"), hex("#9A4B42"), hex("#5E6E2E"), hex("#3F5F8A")}},
}

// ThemeNamed is the theme called name, or the default.
func ThemeNamed(name string) int {
	for i, t := range Themes {
		if strings.EqualFold(t.Name, name) {
			return i
		}
	}
	return 0
}

func style(c rgb) lipgloss.Style  { return lipgloss.NewStyle().Foreground(lipgloss.Color(c.String())) }
func fg(c rgb, s string) string   { return style(c).Render(s) }
func bold(c rgb, s string) string { return style(c).Bold(true).Render(s) }
func on(c rgb, bg *rgb, s string) string {
	st := style(c)
	if bg != nil {
		st = st.Background(lipgloss.Color(bg.String()))
	}
	return st.Render(s)
}

func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

func spread(left, right string, w int) string {
	return left + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 1)) + right
}

func dur(n int) string {
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh %02dm", n/60, n%60)
}

func signed(n int) string {
	if n < 0 {
		return "-" + dur(-n)
	}
	return "+" + dur(n)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// box draws a panel with its number and title set into the top border.
func (th *Theme) box(n int, title, right string, focused bool, w, h int, body []string) []string {
	e := th.border
	if focused {
		e = th.focus
	}
	t := " " + fg(th.dim, fmt.Sprint(n)) + " " + bold(th.text, title) + " "
	r := ""
	if right != "" {
		r = " " + right + " "
	}
	fill := w - 4 - lipgloss.Width(t) - lipgloss.Width(r)
	out := []string{fg(e, "╭─") + t + fg(e, strings.Repeat("─", max(fill, 0))) + r + fg(e, "─╮")}
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, fg(e, "│")+pad(line, w-2)+fg(e, "│"))
	}
	return append(out, fg(e, "╰"+strings.Repeat("─", w-2)+"╯"))
}
