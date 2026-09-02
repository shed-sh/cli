package diag

// EditDistance is the Levenshtein distance between two short words. It backs
// every "did you mean" suggestion — mistyped commands and mistyped keyword
// arguments alike — so all of them agree on what counts as close.
func EditDistance(from, to string) int {
	previous := make([]int, len(to)+1)
	current := make([]int, len(to)+1)
	for index := range previous {
		previous[index] = index
	}
	for fromIndex := 1; fromIndex <= len(from); fromIndex++ {
		current[0] = fromIndex
		for toIndex := 1; toIndex <= len(to); toIndex++ {
			cost := 1
			if from[fromIndex-1] == to[toIndex-1] {
				cost = 0
			}
			current[toIndex] = min(previous[toIndex]+1, min(current[toIndex-1]+1, previous[toIndex-1]+cost))
		}
		previous, current = current, previous
	}
	return previous[len(to)]
}
