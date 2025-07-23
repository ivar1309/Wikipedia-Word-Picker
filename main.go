package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	_ "modernc.org/sqlite"
)

var randomArticleURLByLanguage = map[string]string{
	"en": "https://en.wikipedia.org/wiki/Special:Random",
	"fr": "https://fr.wikipedia.org/wiki/Sp%C3%A9cial:Page_au_hasard",
	"de": "https://de.wikipedia.org/wiki/Spezial:Zuf%C3%A4llige_Seite",
}

type Response struct {
	Language string   `json:"language"`
	Words    []string `json:"words"`
}

var db *sql.DB

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "words.db")
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS used_words (word TEXT,language TEXT,PRIMARY KEY(word, language))`)
	return err
}

func storeUsedWords(words []string, language string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO used_words(word,language) VALUES (?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, word := range words {
		if _, err := stmt.Exec(word, language); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func getUsedWords(language string) (map[string]struct{}, error) {
	rows, err := db.Query("SELECT word FROM used_words WHERE language=?", language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	used := make(map[string]struct{})
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			return nil, err
		}
		used[word] = struct{}{}
	}
	return used, nil
}

// ExtractWordsFromParagraphs parses HTML content, extracts text from <p> tags,
// and returns a slice of all words found within those paragraphs.
func ExtractWordsFromParagraphs(htmlContent string) ([]string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var words []string

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "p" {
			text := RemovePunctuation(getText(n))
			words = append(words, strings.Fields(text)...)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(doc)

	return words, nil
}

// getText recursively retrieves all text content within a node.
func getText(n *html.Node) string {
	var builder strings.Builder
	if n.Type == html.TextNode {
		builder.WriteString(n.Data)
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		builder.WriteString(getText(c))
	}

	return builder.String()
}

// RemovePunctuation removes all punctuation and special characters from a string,
// keeping only letters, whitespace and apostrophes.
func RemovePunctuation(s string) string {
	var builder strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsSpace(r) || r == '\'' {
			builder.WriteRune(unicode.ToLower(r))
		}
	}

	return builder.String()
}

// Check if a word is in an array.
func contains(words []string, word string) bool {
	for _, value := range words {
		if value == word {
			return true
		}
	}

	return false
}

// PickRandomUniqueWords returns n unique random words from the input slice.
// If n > len(words), it returns all words.
func PickRandomUniqueWords(words []string, n int, usedBefore map[string]struct{}) []string {
	if n >= len(words) {
		return words
	}

	randomWords := make([]string, 0, n)

	for {
		word := words[rand.Intn(len(words))]
		if _, used := usedBefore[word]; used || contains(randomWords, word) {
			continue
		}

		randomWords = append(randomWords, word)

		if len(randomWords) == n {
			break
		}
	}

	return randomWords
}

func pickHandler(w http.ResponseWriter, r *http.Request) {
	language := r.URL.Query().Get("language")
	if language == "" {
		language = "en"
	}

	count := r.URL.Query().Get("count")
	if count == "" {
		count = "10"
	}

	countValue, err := strconv.Atoi(count)
	if err != nil {
		countValue = 10
	}

	resp, err := http.Get(randomArticleURLByLanguage[language])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	builder := new(strings.Builder)
	_, err = builder.Write(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	words, err := ExtractWordsFromParagraphs(builder.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	usedBefore, err := getUsedWords(language)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	firstNWords := PickRandomUniqueWords(words, countValue, usedBefore)

	err = storeUsedWords(firstNWords, language)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := Response{
		Language: language,
		Words:    firstNWords,
	}
	//fmt.Println(words)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func main() {
	initDB()
	http.HandleFunc("/pick", pickHandler)

	log.Print("Listening on port: 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
