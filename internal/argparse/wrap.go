package argparse

import (
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"

	"kenttheclerk/internal/py"
)

// terminalColumns is os.get_terminal_size(sys.__stdout__.fileno()).columns, or 0 when stdout is not a
// terminal.
func terminalColumns() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// wrap is textwrap.wrap(text, width) with the defaults argparse relies on: whitespace already
// collapsed, words broken after hyphens, long words broken, whitespace dropped at line edges.
func wrap(text string, width int) []string {
	chunks := splitChunks([]rune(text))
	// reversed stack, as textwrap keeps it
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
	var lines []string
	for len(chunks) > 0 {
		var cur [][]rune
		curLen := 0
		if strings.TrimSpace(string(chunks[len(chunks)-1])) == "" && len(lines) > 0 {
			chunks = chunks[:len(chunks)-1]
		}
		for len(chunks) > 0 {
			l := len(chunks[len(chunks)-1])
			if curLen+l <= width {
				cur = append(cur, chunks[len(chunks)-1])
				chunks = chunks[:len(chunks)-1]
				curLen += l
			} else {
				break
			}
		}
		if len(chunks) > 0 && len(chunks[len(chunks)-1]) > width {
			// _handle_long_word
			spaceLeft := width - curLen
			if width < 1 {
				spaceLeft = 1
			}
			chunk := chunks[len(chunks)-1]
			end := spaceLeft
			if len(chunk) > spaceLeft {
				hyphen := -1
				for k := min(spaceLeft, len(chunk)) - 1; k >= 0; k-- {
					if chunk[k] == '-' {
						hyphen = k
						break
					}
				}
				if hyphen > 0 && strings.Trim(string(chunk[:hyphen]), "-") != "" {
					end = hyphen + 1
				}
			}
			cur = append(cur, chunk[:end])
			chunks[len(chunks)-1] = chunk[end:]
			curLen = 0
			for _, c := range cur {
				curLen += len(c)
			}
		}
		if len(cur) > 0 && strings.TrimSpace(string(cur[len(cur)-1])) == "" {
			cur = cur[:len(cur)-1]
		}
		if len(cur) > 0 {
			var b strings.Builder
			for _, c := range cur {
				b.WriteString(string(c))
			}
			lines = append(lines, b.String())
		}
	}
	return lines
}

func isWS(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' }

// letter is [^\d\W]: a word character that is not a decimal digit.
func letter(r rune) bool { return py.IsWord(r) && !py.IsDecimal(r) }

// wordPunct is [\w!"'&.,?].
func wordPunct(r rune) bool { return py.IsWord(r) || strings.ContainsRune(`!"'&.,?`, r) }

// splitChunks is TextWrapper._split with break_on_hyphens: textwrap's wordsep_re, by hand.
func splitChunks(s []rune) [][]rune {
	var out [][]rune
	n := len(s)
	at := func(k int) rune {
		if k < 0 || k >= n {
			return -1
		}
		return s[k]
	}
	dashesThenWord := func(k int) int { // length of -{2,} followed by \w at k, else 0
		j := k
		for j < n && s[j] == '-' {
			j++
		}
		if j-k >= 2 && j < n && py.IsWord(s[j]) {
			return j - k
		}
		return 0
	}
	for i := 0; i < n; {
		if isWS(s[i]) {
			j := i
			for j < n && isWS(s[j]) {
				j++
			}
			out = append(out, s[i:j])
			i = j
			continue
		}
		// em-dash between words
		if i > 0 && wordPunct(s[i-1]) {
			if l := dashesThenWord(i); l > 0 {
				out = append(out, s[i:i+l])
				i += l
				continue
			}
		}
		// word, possibly hyphenated: shortest run of non-whitespace followed by one of the endings
		end := -1
		for j := i + 1; j <= n; j++ {
			if j-1 >= i && isWS(s[j-1]) {
				break
			}
			// hyphenated word: the run ends with '-' preceded by two letters or letter-letter-, and
			// followed by letter, optional '-', letter
			if at(j) == '-' {
				lb := (letter(at(j-2)) && letter(at(j-1))) ||
					(letter(at(j-3)) && at(j-2) == '-' && letter(at(j-1)))
				la := letter(at(j+1)) && (letter(at(j+2)) || (at(j+2) == '-' && letter(at(j+3))))
				if lb && la {
					end = j + 1
					break
				}
			}
			if j == n || isWS(s[j]) {
				end = j
				break
			}
			if wordPunct(s[j-1]) && dashesThenWord(j) > 0 {
				end = j
				break
			}
		}
		if end < 0 {
			end = i + 1
		}
		out = append(out, s[i:end])
		i = end
	}
	return out
}

var _ = unicode.IsSpace
