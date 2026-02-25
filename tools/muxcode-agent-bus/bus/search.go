package bus

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// SearchMode controls which scoring algorithm is used.
type SearchMode int

const (
	// SearchModeKeyword uses the legacy substring-counting scorer.
	SearchModeKeyword SearchMode = iota
	// SearchModeBM25 uses Okapi BM25 with IDF weighting and length normalization.
	SearchModeBM25
)

// SearchOptions configures a memory search.
type SearchOptions struct {
	Query      string
	RoleFilter string
	Limit      int
	Mode       SearchMode
}

// corpus holds collection-level statistics for BM25 scoring.
type corpus struct {
	docCount  int
	avgDocLen float64
	docFreq   map[string]int // term -> number of docs containing it
}

// tokenizedEntry holds pre-tokenized text for a memory entry.
type tokenizedEntry struct {
	headerTokens  []string
	contentTokens []string
	totalLen      int
}

// BM25 tuning constants
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// stopWords are common English words filtered during tokenization.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "by": true, "do": true, "for": true,
	"from": true, "had": true, "has": true, "have": true, "he": true,
	"her": true, "his": true, "how": true, "i": true, "if": true,
	"in": true, "is": true, "it": true, "its": true, "me": true,
	"my": true, "no": true, "not": true, "of": true, "on": true,
	"or": true, "our": true, "out": true, "she": true, "so": true,
	"than": true, "that": true, "the": true, "then": true, "them": true,
	"they": true, "this": true, "to": true, "up": true, "us": true,
	"was": true, "we": true, "were": true, "what": true, "when": true,
	"which": true, "who": true, "will": true, "with": true, "you": true,
	"your": true,
}

// tokenize splits text into lowercase tokens, filtering stop words and short tokens.
func tokenize(text string) []string {
	lower := strings.ToLower(text)
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var tokens []string
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		if stopWords[w] {
			continue
		}
		tokens = append(tokens, stem(w))
	}
	return tokens
}

// stem applies simple suffix stripping to approximate stemming.
// Words under 4 characters pass through unchanged.
func stem(word string) string {
	if len(word) < 4 {
		return word
	}

	// Try suffixes longest-first to avoid partial matches
	suffixes := []string{
		"tion", "ment", "ness", "ing", "ies",
		"est", "ely", "ed", "ly", "er", "es",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(word, suffix) {
			trimmed := word[:len(word)-len(suffix)]
			if len(trimmed) >= 2 {
				return trimmed
			}
		}
	}

	// Strip trailing 's' if result is still >= 3 chars
	if strings.HasSuffix(word, "s") && len(word) > 3 {
		return word[:len(word)-1]
	}

	return word
}

// buildCorpus computes collection-level statistics from a set of entries.
func buildCorpus(entries []MemoryEntry) corpus {
	c := corpus{
		docCount: len(entries),
		docFreq:  make(map[string]int),
	}
	if c.docCount == 0 {
		return c
	}

	totalLen := 0
	for _, entry := range entries {
		te := tokenizeEntry(entry)
		totalLen += te.totalLen

		// Count unique terms per document for document frequency
		seen := make(map[string]bool)
		for _, t := range te.headerTokens {
			seen[t] = true
		}
		for _, t := range te.contentTokens {
			seen[t] = true
		}
		for t := range seen {
			c.docFreq[t]++
		}
	}

	c.avgDocLen = float64(totalLen) / float64(c.docCount)
	return c
}

// tokenizeEntry pre-tokenizes a memory entry's header and content.
func tokenizeEntry(entry MemoryEntry) tokenizedEntry {
	ht := tokenize(entry.Section)
	ct := tokenize(entry.Content)
	return tokenizedEntry{
		headerTokens:  ht,
		contentTokens: ct,
		totalLen:      len(ht) + len(ct),
	}
}

// bm25Score computes the BM25 relevance score for a tokenized entry.
// Header term frequencies are weighted 2x.
func bm25Score(entry tokenizedEntry, queryTerms []string, corp corpus) float64 {
	if corp.docCount == 0 || corp.avgDocLen == 0 {
		return 0
	}

	// Count term frequencies with header 2x weight
	tf := make(map[string]float64)
	for _, t := range entry.headerTokens {
		tf[t] += 2.0
	}
	for _, t := range entry.contentTokens {
		tf[t] += 1.0
	}

	// Effective document length (header tokens count double)
	docLen := float64(len(entry.headerTokens))*2.0 + float64(len(entry.contentTokens))

	var score float64
	for _, qt := range queryTerms {
		termTF := tf[qt]
		if termTF == 0 {
			continue
		}

		// IDF: log((N - df + 0.5) / (df + 0.5) + 1)
		df := float64(corp.docFreq[qt])
		idf := math.Log((float64(corp.docCount)-df+0.5)/(df+0.5) + 1.0)

		// BM25 term score
		numerator := termTF * (bm25K1 + 1.0)
		denominator := termTF + bm25K1*(1.0-bm25B+bm25B*docLen/corp.avgDocLen)
		score += idf * (numerator / denominator)
	}

	return score
}

// parseQuery splits a query string into individual terms and quoted phrases.
// Quoted substrings (e.g., `"cdk diff"`) are returned as phrases.
func parseQuery(query string) (terms []string, phrases [][]string) {
	lower := strings.ToLower(query)

	var current []rune
	inQuote := false
	var phraseWords []string

	for _, r := range lower {
		if r == '"' {
			if inQuote {
				// End of quoted phrase
				word := strings.TrimSpace(string(current))
				if word != "" {
					phraseWords = append(phraseWords, stem(word))
				}
				if len(phraseWords) > 0 {
					phrases = append(phrases, phraseWords)
					// Also add phrase words as individual terms
					terms = append(terms, phraseWords...)
				}
				phraseWords = nil
				current = nil
			}
			inQuote = !inQuote
			continue
		}

		if inQuote {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				current = append(current, r)
			} else {
				word := strings.TrimSpace(string(current))
				if word != "" && !stopWords[word] && len(word) >= 2 {
					phraseWords = append(phraseWords, stem(word))
				}
				current = nil
			}
		} else {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				current = append(current, r)
			} else {
				word := strings.TrimSpace(string(current))
				if word != "" && !stopWords[word] && len(word) >= 2 {
					terms = append(terms, stem(word))
				}
				current = nil
			}
		}
	}

	// Flush remaining
	word := strings.TrimSpace(string(current))
	if word != "" && !stopWords[word] && len(word) >= 2 {
		if inQuote {
			phraseWords = append(phraseWords, stem(word))
			if len(phraseWords) > 0 {
				phrases = append(phrases, phraseWords)
				terms = append(terms, phraseWords...)
			}
		} else {
			terms = append(terms, stem(word))
		}
	}

	return terms, phrases
}

// phraseBonus returns a bonus multiplier for exact phrase matches.
// Returns 2.0 for each phrase found in the entry, 0.0 otherwise.
func phraseBonus(entry tokenizedEntry, phrases [][]string) float64 {
	if len(phrases) == 0 {
		return 0
	}

	allTokens := append(entry.headerTokens, entry.contentTokens...)
	var bonus float64

	for _, phrase := range phrases {
		if len(phrase) == 0 {
			continue
		}
		if containsPhrase(allTokens, phrase) {
			bonus += 2.0
		}
	}

	return bonus
}

// containsPhrase checks if a token sequence contains a phrase subsequence.
func containsPhrase(tokens, phrase []string) bool {
	if len(phrase) > len(tokens) {
		return false
	}
	for i := 0; i <= len(tokens)-len(phrase); i++ {
		match := true
		for j, p := range phrase {
			if tokens[i+j] != p {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// SearchMemoryBM25 searches all memory entries using BM25 ranking.
func SearchMemoryBM25(opts SearchOptions) ([]SearchResult, error) {
	entries, err := AllMemoryEntries()
	if err != nil {
		return nil, err
	}

	queryTerms, phrases := parseQuery(opts.Query)
	if len(queryTerms) == 0 {
		return nil, nil
	}

	// Filter by role before building corpus for accurate IDF
	var filtered []MemoryEntry
	for _, entry := range entries {
		if opts.RoleFilter != "" && entry.Role != opts.RoleFilter {
			continue
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == 0 {
		return nil, nil
	}

	corp := buildCorpus(filtered)

	var results []SearchResult
	for _, entry := range filtered {
		te := tokenizeEntry(entry)
		score := bm25Score(te, queryTerms, corp)
		if score > 0 {
			score += phraseBonus(te, phrases)
			results = append(results, SearchResult{Entry: entry, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}

	return results, nil
}

// SearchMemoryWithOptions dispatches to BM25 or keyword search based on mode.
func SearchMemoryWithOptions(opts SearchOptions) ([]SearchResult, error) {
	switch opts.Mode {
	case SearchModeBM25:
		return SearchMemoryBM25(opts)
	default:
		return SearchMemory(opts.Query, opts.RoleFilter, opts.Limit)
	}
}
