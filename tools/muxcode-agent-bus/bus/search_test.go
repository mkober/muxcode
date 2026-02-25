package bus

import (
	"fmt"
	"testing"
)

func TestTokenize(t *testing.T) {
	tokens := tokenize("Build the CDK deploy pipeline")
	// "the" is a stop word, should be filtered
	for _, tok := range tokens {
		if tok == "the" {
			t.Error("stop word 'the' should be filtered")
		}
	}
	if len(tokens) == 0 {
		t.Fatal("expected non-empty tokens")
	}
	// Should contain stemmed forms
	found := map[string]bool{}
	for _, tok := range tokens {
		found[tok] = true
	}
	if !found["build"] {
		t.Error("expected 'build' token")
	}
	if !found["cdk"] {
		t.Error("expected 'cdk' token")
	}
	if !found["deploy"] {
		t.Error("expected 'deploy' token")
	}
}

func TestTokenize_Empty(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens for empty string, got %d", len(tokens))
	}
}

func TestTokenize_StopWordsOnly(t *testing.T) {
	tokens := tokenize("the and is of to in for")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens for stop-words-only input, got %d: %v", len(tokens), tokens)
	}
}

func TestStem(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"building", "build"},
		{"deployed", "deploy"},
		{"quickly", "quick"},
		{"configuration", "configura"},
		{"testing", "test"},
		{"running", "runn"},
		{"services", "servic"},
		{"largest", "larg"},
		{"deployment", "deploy"},
		{"awareness", "aware"},
	}
	for _, tc := range tests {
		got := stem(tc.input)
		if got != tc.want {
			t.Errorf("stem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStem_ShortWords(t *testing.T) {
	// Words under 4 chars should pass through
	shorts := []string{"the", "is", "go", "cdk", "aws"}
	for _, w := range shorts {
		got := stem(w)
		if got != w {
			t.Errorf("stem(%q) = %q, want unchanged", w, got)
		}
	}
}

func TestBuildCorpus(t *testing.T) {
	entries := []MemoryEntry{
		{Role: "build", Section: "Config", Content: "use pnpm for builds"},
		{Role: "build", Section: "Deploy", Content: "run cdk diff first"},
		{Role: "shared", Section: "Notes", Content: "always deploy carefully"},
	}

	corp := buildCorpus(entries)
	if corp.docCount != 3 {
		t.Errorf("docCount: got %d, want 3", corp.docCount)
	}
	if corp.avgDocLen <= 0 {
		t.Errorf("avgDocLen should be positive, got %f", corp.avgDocLen)
	}
	// "deploy" appears in 2 docs (Deploy header + Notes content)
	deployTerm := stem("deploy")
	if corp.docFreq[deployTerm] != 2 {
		t.Errorf("docFreq[%q]: got %d, want 2", deployTerm, corp.docFreq[deployTerm])
	}
}

func TestBM25Score_SingleTerm(t *testing.T) {
	entries := []MemoryEntry{
		{Section: "Config", Content: "use pnpm for all builds"},
		{Section: "Notes", Content: "deploy the application"},
	}
	corp := buildCorpus(entries)

	te := tokenizeEntry(entries[0])
	score := bm25Score(te, []string{"pnpm"}, corp)
	if score <= 0 {
		t.Errorf("expected positive score for matching term, got %f", score)
	}

	// Non-matching entry should score 0
	te2 := tokenizeEntry(entries[1])
	score2 := bm25Score(te2, []string{"pnpm"}, corp)
	if score2 != 0 {
		t.Errorf("expected 0 score for non-matching term, got %f", score2)
	}
}

func TestBM25Score_RareTerm(t *testing.T) {
	// Rare term should score higher than common term
	entries := []MemoryEntry{
		{Section: "Config", Content: "use pnpm for builds and deploy"},
		{Section: "Deploy", Content: "deploy the app with cdk"},
		{Section: "Notes", Content: "deploy notes for reference"},
	}
	corp := buildCorpus(entries)

	te := tokenizeEntry(entries[0])
	// "pnpm" is rare (1 doc), "deploy" is common (3 docs)
	pnpmScore := bm25Score(te, []string{"pnpm"}, corp)
	deployScore := bm25Score(te, []string{stem("deploy")}, corp)

	if pnpmScore <= deployScore {
		t.Errorf("rare term 'pnpm' (%.3f) should score higher than common 'deploy' (%.3f)",
			pnpmScore, deployScore)
	}
}

func TestBM25Score_HeaderBoost(t *testing.T) {
	// Entry with term in header should outscore term in body only
	headerEntry := MemoryEntry{Section: "Deploy Guide", Content: "follow the steps"}
	bodyEntry := MemoryEntry{Section: "General Notes", Content: "remember to deploy"}

	entries := []MemoryEntry{headerEntry, bodyEntry}
	corp := buildCorpus(entries)

	te1 := tokenizeEntry(headerEntry)
	te2 := tokenizeEntry(bodyEntry)
	deployTerm := stem("deploy")

	score1 := bm25Score(te1, []string{deployTerm}, corp)
	score2 := bm25Score(te2, []string{deployTerm}, corp)

	if score1 <= score2 {
		t.Errorf("header match (%.3f) should outscore body-only match (%.3f)", score1, score2)
	}
}

func TestBM25Score_LongDoc(t *testing.T) {
	// BM25 should penalize long documents via length normalization
	shortEntry := MemoryEntry{Section: "Short", Content: "deploy"}
	longContent := "deploy"
	for i := 0; i < 50; i++ {
		longContent += " filler word padding extra"
	}
	longEntry := MemoryEntry{Section: "Long", Content: longContent}

	entries := []MemoryEntry{shortEntry, longEntry}
	corp := buildCorpus(entries)

	te1 := tokenizeEntry(shortEntry)
	te2 := tokenizeEntry(longEntry)
	deployTerm := stem("deploy")

	score1 := bm25Score(te1, []string{deployTerm}, corp)
	score2 := bm25Score(te2, []string{deployTerm}, corp)

	if score1 <= score2 {
		t.Errorf("short doc (%.3f) should outscore long doc (%.3f) for same term frequency",
			score1, score2)
	}
}

func TestParseQuery_Simple(t *testing.T) {
	terms, phrases := parseQuery("cdk deploy")
	if len(terms) != 2 {
		t.Fatalf("expected 2 terms, got %d: %v", len(terms), terms)
	}
	if len(phrases) != 0 {
		t.Errorf("expected 0 phrases, got %d", len(phrases))
	}
	if terms[0] != "cdk" {
		t.Errorf("first term: got %q, want 'cdk'", terms[0])
	}
}

func TestParseQuery_Quoted(t *testing.T) {
	terms, phrases := parseQuery(`"cdk diff" deploy`)
	if len(phrases) != 1 {
		t.Fatalf("expected 1 phrase, got %d", len(phrases))
	}
	if len(phrases[0]) != 2 {
		t.Fatalf("expected 2-word phrase, got %d: %v", len(phrases[0]), phrases[0])
	}
	if phrases[0][0] != "cdk" || phrases[0][1] != "diff" {
		t.Errorf("phrase: got %v, want [cdk diff]", phrases[0])
	}
	// Phrase words should also appear as individual terms
	foundDeploy := false
	for _, term := range terms {
		if term == stem("deploy") {
			foundDeploy = true
		}
	}
	if !foundDeploy {
		t.Errorf("expected 'deploy' term, terms: %v", terms)
	}
}

func TestPhraseBonus(t *testing.T) {
	entry := tokenizedEntry{
		headerTokens:  []string{"cdk", "diff"},
		contentTokens: []string{"run", "cdk", "diff", "first"},
	}

	// Matching phrase should give bonus
	bonus := phraseBonus(entry, [][]string{{"cdk", "diff"}})
	if bonus <= 0 {
		t.Errorf("expected positive phrase bonus, got %f", bonus)
	}

	// Non-matching phrase should give no bonus
	bonus2 := phraseBonus(entry, [][]string{{"pnpm", "build"}})
	if bonus2 != 0 {
		t.Errorf("expected 0 bonus for non-matching phrase, got %f", bonus2)
	}
}

func TestPhraseBonus_NoPhrase(t *testing.T) {
	entry := tokenizedEntry{
		headerTokens:  []string{"config"},
		contentTokens: []string{"use", "pnpm"},
	}
	bonus := phraseBonus(entry, nil)
	if bonus != 0 {
		t.Errorf("expected 0 bonus with no phrases, got %f", bonus)
	}
}

func TestSearchMemoryBM25_BasicMatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm for all builds", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	if err := AppendMemory("Deploy Notes", "always run cdk diff first", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: "pnpm"})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entry.Section != "Build Config" {
		t.Errorf("expected 'Build Config', got %q", results[0].Entry.Section)
	}
}

func TestSearchMemoryBM25_RankingVsKeyword(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Entry with rare term "pnpm" in header
	if err := AppendMemory("PNPM Config", "package manager setup", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	// Entry with common term "config" repeated many times
	if err := AppendMemory("General", "config config config config config", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	// BM25 should rank the PNPM entry higher for "pnpm config"
	// because "pnpm" is rare and has IDF boost
	results, err := SearchMemoryBM25(SearchOptions{Query: "pnpm config"})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Entry.Section != "PNPM Config" {
		t.Errorf("BM25 should rank 'PNPM Config' first (rare term + header), got %q",
			results[0].Entry.Section)
	}
}

func TestSearchMemoryBM25_RoleFilter(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Config", "use pnpm", "build"); err != nil {
		t.Fatalf("AppendMemory build: %v", err)
	}
	if err := AppendMemory("Config", "use pnpm too", "shared"); err != nil {
		t.Fatalf("AppendMemory shared: %v", err)
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: "pnpm", RoleFilter: "build"})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with role filter, got %d", len(results))
	}
	if results[0].Entry.Role != "build" {
		t.Errorf("expected role 'build', got %q", results[0].Entry.Role)
	}
}

func TestSearchMemoryBM25_Limit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	for i := 0; i < 5; i++ {
		section := fmt.Sprintf("Entry %d", i)
		if err := AppendMemory(section, "common keyword here", "shared"); err != nil {
			t.Fatalf("AppendMemory %d: %v", i, err)
		}
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: "keyword", Limit: 2})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit, got %d", len(results))
	}
}

func TestSearchMemoryBM25_NoMatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: "nonexistent"})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchMemoryBM25_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use PNPM always", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: "pnpm"})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestSearchMemoryWithOptions_ModeKeywordBackcompat(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm for all builds", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	// Keyword mode should work the same as legacy SearchMemory
	results, err := SearchMemoryWithOptions(SearchOptions{
		Query: "pnpm",
		Mode:  SearchModeKeyword,
	})
	if err != nil {
		t.Fatalf("SearchMemoryWithOptions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result in keyword mode, got %d", len(results))
	}
	if results[0].Entry.Section != "Build Config" {
		t.Errorf("expected 'Build Config', got %q", results[0].Entry.Section)
	}
}

func TestSearchMemoryWithOptions_ModeBM25(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm for all builds", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemoryWithOptions(SearchOptions{
		Query: "pnpm",
		Mode:  SearchModeBM25,
	})
	if err != nil {
		t.Fatalf("SearchMemoryWithOptions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result in BM25 mode, got %d", len(results))
	}
}

func TestContainsPhrase(t *testing.T) {
	tokens := []string{"run", "cdk", "diff", "first"}

	if !containsPhrase(tokens, []string{"cdk", "diff"}) {
		t.Error("expected phrase [cdk diff] to be found")
	}
	if containsPhrase(tokens, []string{"diff", "cdk"}) {
		t.Error("phrase [diff cdk] should not match (wrong order)")
	}
	if containsPhrase(tokens, []string{"cdk", "diff", "second"}) {
		t.Error("longer phrase should not match")
	}
	if !containsPhrase(tokens, []string{"run"}) {
		t.Error("single-word phrase should match")
	}
}

func TestSearchMemoryBM25_PhraseSearch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Entry with exact phrase "cdk diff"
	if err := AppendMemory("Deploy Guide", "always run cdk diff first", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	// Entry with both words but not as phrase
	if err := AppendMemory("CDK Notes", "the diff tool is useful for cdk projects", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemoryBM25(SearchOptions{Query: `"cdk diff"`})
	if err != nil {
		t.Fatalf("SearchMemoryBM25: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Phrase match should rank higher
	if results[0].Entry.Section != "Deploy Guide" {
		t.Errorf("phrase match should rank first, got %q", results[0].Entry.Section)
	}
}
