package quality

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type grid struct {
	rows  [][]bool
	width int
	style string
}

func (g grid) aspect() float64 { return float64(g.width) / float64(len(g.rows)) }

func normalize(raw, target string) (grid, string) {
	if len(raw) > MaxResponseBytes {
		return grid{}, "input_limit"
	}
	if !utf8.ValidString(raw) {
		return grid{}, "invalid_utf8"
	}
	if strings.Contains(raw, target) && target != "" {
		return grid{}, "copied_target"
	}
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) > MaxRows {
		return grid{}, "row_limit"
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return grid{}, "empty_grid"
	}
	first := strings.TrimSpace(lines[0])
	if strings.HasPrefix(first, "```") || strings.HasPrefix(first, "~~~") {
		fence := first[:3]
		if first != fence && first != fence+"text" && first != fence+"ascii" && first != fence+"plaintext" {
			return grid{}, "invalid_fence"
		}
		if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != fence {
			return grid{}, "invalid_fence"
		}
		lines = lines[1 : len(lines)-1]
	}
	for _, line := range lines {
		if strings.Contains(line, "```") || strings.Contains(line, "~~~") {
			return grid{}, "invalid_fence"
		}
	}
	rows := make([][]bool, 0, len(lines))
	left, right, top, bottom := MaxColumns, -1, -1, -1
	free := false
	letters := make(map[rune]bool)
	palette := make(map[rune]bool)
	for y, line := range lines {
		row := make([]bool, 0, len(line))
		for _, ch := range line {
			if ch >= 0xff01 && ch <= 0xff5e {
				ch -= 0xfee0
			}
			if ch == 0x3000 {
				ch = ' '
			}
			if ch == '\t' {
				for n := 4 - len(row)%4; n > 0; n-- {
					row = append(row, false)
				}
			} else {
				if ch != ' ' && !drawingRune(ch) {
					return grid{}, "non_grid_text"
				}
				if ch != ' ' && !strings.ContainsRune("#*+@%█▓▒■●─━│┃┌┐└┘├┤┬┴┼═║╔╗╚╝╠╣╦╩╬-_|/\\=", ch) {
					free = true
				}
				if unicode.IsLetter(ch) || unicode.IsDigit(ch) {
					letters[unicode.ToLower(ch)] = true
				}
				if ch != ' ' {
					palette[ch] = true
				}
				row = append(row, ch != ' ')
			}
			if len(row) > MaxColumns {
				return grid{}, "column_limit"
			}
		}
		for x, ink := range row {
			if ink {
				left, right = min(left, x), max(right, x)
				if top < 0 {
					top = y
				}
				bottom = y
			}
		}
		rows = append(rows, row)
	}
	if len(letters) > 2 {
		return grid{}, "plain_text"
	}
	if right < 0 {
		return grid{}, "empty_grid"
	}
	width := right - left + 1
	cropped := make([][]bool, bottom-top+1)
	for y := range cropped {
		cropped[y] = make([]bool, width)
		for x := range cropped[y] {
			if left+x < len(rows[top+y]) {
				cropped[y][x] = rows[top+y][left+x]
			}
		}
	}
	// Equal blank row spacing is a presentation convention, not extra strokes.
	spacing, previous, uniform := -1, -1, true
	for y, row := range cropped {
		occupied := false
		for _, ink := range row {
			occupied = occupied || ink
		}
		if !occupied {
			continue
		}
		if previous >= 0 {
			gap := y - previous - 1
			if spacing < 0 {
				spacing = gap
			} else if spacing != gap {
				uniform = false
			}
		}
		previous = y
	}
	if uniform && spacing > 0 {
		compact := make([][]bool, 0, len(cropped))
		for y := 0; y < len(cropped); y += spacing + 1 {
			compact = append(compact, cropped[y])
		}
		cropped = compact
	}
	spacing, previous, uniform = -1, -1, true
	for x := range width {
		occupied := false
		for _, row := range cropped {
			occupied = occupied || row[x]
		}
		if !occupied {
			continue
		}
		if previous >= 0 {
			gap := x - previous - 1
			if spacing < 0 {
				spacing = gap
			} else if spacing != gap {
				uniform = false
			}
		}
		previous = x
	}
	if uniform && spacing > 0 {
		for y, row := range cropped {
			compact := make([]bool, 0, width)
			for x := 0; x < width; x += spacing + 1 {
				compact = append(compact, row[x])
			}
			cropped[y] = compact
		}
		width = len(cropped[0])
	}
	style := "solid"
	if free {
		style = "free"
	}
	inkRunes := make([]rune, 0, len(palette))
	for ch := range palette {
		inkRunes = append(inkRunes, ch)
	}
	sort.Slice(inkRunes, func(i, j int) bool { return inkRunes[i] < inkRunes[j] })
	style += ":" + digest([]byte(string(inkRunes)))
	return grid{rows: cropped, width: width, style: style}, ""
}

func drawingRune(ch rune) bool {
	return ch >= 0x21 && ch <= 0x7e || strings.ContainsRune("█▓▒░■□●○─━│┃┌┐└┘├┤┬┴┼═║╔╗╚╝╠╣╦╩╬", ch)
}

type bitmap [32][32]bool

func render(g grid) bitmap {
	var out bitmap
	scale := 28 / float64(max(g.width, len(g.rows)))
	offsetX := (32 - float64(g.width)*scale) / 2
	offsetY := (32 - float64(len(g.rows))*scale) / 2
	for y, row := range g.rows {
		for x, ink := range row {
			if !ink {
				continue
			}
			x0, x1 := int(math.Floor(offsetX+float64(x)*scale)), int(math.Ceil(offsetX+float64(x+1)*scale))
			y0, y1 := int(math.Floor(offsetY+float64(y)*scale)), int(math.Ceil(offsetY+float64(y+1)*scale))
			for py := max(0, y0); py < min(32, y1); py++ {
				for px := max(0, x0); px < min(32, x1); px++ {
					out[py][px] = true
				}
			}
		}
	}
	return out
}

func compare(a, b bitmap, aspectA, aspectB float64) (Components, float64) {
	best := -1.0
	var parts Components
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			var rowsA, rowsB, colsA, colsB [32]int
			na, nb, intersect := 0, 0, 0
			for y := range 32 {
				for x := range 32 {
					ink := y-dy >= 0 && y-dy < 32 && x-dx >= 0 && x-dx < 32 && a[y-dy][x-dx]
					if ink {
						na++
						rowsA[y]++
						colsA[x]++
					}
					if b[y][x] {
						nb++
						rowsB[y]++
						colsB[x]++
					}
					if ink && b[y][x] {
						intersect++
					}
				}
			}
			p := Components{
				Dice:       2 * float64(intersect) / float64(na+nb),
				Projection: (projection(rowsA, rowsB) + projection(colsA, colsB)) / 2,
				Layout:     math.Min(aspectA, aspectB) / math.Max(aspectA, aspectB),
			}
			score := .7*p.Dice + .2*p.Projection + .1*p.Layout
			if score > best {
				parts, best = p, score
			}
		}
	}
	return parts, best
}

func projection(a, b [32]int) float64 {
	difference, total := 0, 0
	for i := range 32 {
		difference += abs(a[i] - b[i])
		total += a[i] + b[i]
	}
	if total == 0 {
		return 1
	}
	return 1 - float64(difference)/float64(total)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
