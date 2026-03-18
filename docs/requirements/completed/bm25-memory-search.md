# BM25 Memory Search

Okapi BM25 ranking replaces keyword counting as the default memory search mode.

## Requirements

- IDF weighting scores terms by inverse document frequency across the corpus
- Length normalization prevents long entries from dominating results
- Porter-style stemming reduces words to root forms for broader matching
- Stop word removal filters common English words from scoring
- Quoted phrase matching preserves multi-word terms as exact sequences
- Backward-compatible: `SearchMemoryWithOptions()` accepts mode parameter

## Key files

| File | Purpose |
|------|---------|
| `bus/search.go` | `tokenize()`, `stem()`, `buildCorpus()`, `bm25Score()`, `SearchMemoryBM25()`, `SearchMemoryWithOptions()` |
