package search

// ScoredChunk represents a text chunk with relevance score and pre-tokenized content for Jaccard.
type ScoredChunk struct {
	Content string
	File    string
	Score   float64
	Tokens  []string // pre-tokenized for Jaccard
}

// MMRRerank performs greedy iterative Maximal Marginal Relevance re-ranking.
// MMR = lambda * relevance - (1-lambda) * max_similarity_to_selected.
// Default lambda=0.7. Uses Jaccard similarity on Tokens field.
func MMRRerank(chunks []ScoredChunk, limit int, lambda float64) []ScoredChunk {
	if lambda <= 0 || lambda > 1 {
		lambda = 0.7
	}
	if len(chunks) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(chunks) {
		limit = len(chunks)
	}

	selected := make([]ScoredChunk, 0, limit)
	remaining := make([]int, len(chunks)) // indices into chunks
	for i := range remaining {
		remaining[i] = i
	}

	for len(selected) < limit && len(remaining) > 0 {
		bestIdx := -1
		bestMMR := -1e18
		bestRemPos := -1

		for remPos, chunkIdx := range remaining {
			relevance := chunks[chunkIdx].Score

			// Max similarity to any already-selected chunk
			maxSim := 0.0
			for _, sel := range selected {
				sim := JaccardSimilarity(chunks[chunkIdx].Tokens, sel.Tokens)
				if sim > maxSim {
					maxSim = sim
				}
			}

			mmr := lambda*relevance - (1-lambda)*maxSim
			if mmr > bestMMR {
				bestMMR = mmr
				bestIdx = chunkIdx
				bestRemPos = remPos
			}
		}

		if bestIdx < 0 {
			break
		}

		selected = append(selected, chunks[bestIdx])
		// Remove from remaining by swapping with last
		remaining[bestRemPos] = remaining[len(remaining)-1]
		remaining = remaining[:len(remaining)-1]
	}

	return selected
}

// JaccardSimilarity computes |intersection| / |union| of two token sets.
func JaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	setA := make(map[string]struct{}, len(a))
	for _, tok := range a {
		setA[tok] = struct{}{}
	}

	setB := make(map[string]struct{}, len(b))
	for _, tok := range b {
		setB[tok] = struct{}{}
	}

	intersection := 0
	for tok := range setA {
		if _, ok := setB[tok]; ok {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
