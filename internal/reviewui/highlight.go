package reviewui

import (
	"image/color"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/textclean"
)

type span struct {
	text string
	fg   color.Color
}

const maxHighlight = 20000

var theme = styles.Get("github-dark")

func plain(l review.Line) []span {
	return []span{{text: textclean.Clean(l.Text)}}
}

func highlight(path string, lines []review.Line) [][]span {
	out := make([][]span, len(lines))
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil || len(lines) > maxHighlight {
		for i, l := range lines {
			out[i] = plain(l)
		}
		return out
	}
	lexer = chroma.Coalesce(lexer)
	for _, side := range []review.LineKind{review.Add, review.Del} {
		var idx []int
		var b strings.Builder
		for i, l := range lines {
			if l.Kind == review.Same || l.Kind == side {
				idx = append(idx, i)
				b.WriteString(l.Text)
				b.WriteByte('\n')
			}
		}
		rows := tokenize(lexer, b.String(), len(idx))
		for j, i := range idx {
			if out[i] == nil {
				out[i] = rows[j]
			}
		}
	}
	for i, l := range lines {
		if out[i] == nil {
			out[i] = plain(l)
		}
	}
	return out
}

func tokenize(lexer chroma.Lexer, text string, n int) [][]span {
	rows := make([][]span, n)
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return rows
	}
	r := 0
	for t := it(); t != chroma.EOF; t = it() {
		for k, part := range strings.Split(t.Value, "\n") {
			if k > 0 {
				r++
			}
			if part != "" && r < n {
				rows[r] = append(rows[r], span{text: textclean.Clean(part), fg: tokenColor(t.Type)})
			}
		}
	}
	for i := range rows {
		if rows[i] == nil {
			rows[i] = []span{}
		}
	}
	return rows
}

func tokenColor(t chroma.TokenType) color.Color {
	if c := theme.Get(t).Colour; c.IsSet() {
		return lipgloss.Color(c.String())
	}
	return nil
}
