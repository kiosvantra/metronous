package tui

// clampColumnWidths returns a copy of widths that fits within terminalWidth
// columns (including inter-column spaces as counted by totalWidth). When
// terminalWidth is zero or negative, the original widths slice is returned.
//
// The algorithm starts from the widest columns and gradually shrinks them
// until totalWidth(widths) is less than or equal to terminalWidth, but never
// reduces any column below minColWidth.
func clampColumnWidths(widths []int, terminalWidth int) []int {
	if terminalWidth <= 0 {
		out := make([]int, len(widths))
		copy(out, widths)
		return out
	}

	out := make([]int, len(widths))
	copy(out, widths)

	const minColWidth = 4
	currentTotal := totalWidth(out)
	if currentTotal <= terminalWidth {
		return out
	}

	for currentTotal > terminalWidth {
		// Find the widest column we can still shrink.
		idx := -1
		maxWidth := minColWidth
		for i, w := range out {
			if w > maxWidth {
				maxWidth = w
				idx = i
			}
		}
		if idx == -1 {
			// All columns are already at or below the minimum width.
			break
		}
		out[idx]--
		currentTotal--
	}

	return out
}
