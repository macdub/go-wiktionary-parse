package main

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/macdub/go-colorlog"
	_ "github.com/mattn/go-sqlite3"
	"go-wikitionary-parse/lib/wikitemplates"
)

var (
	// regex pointers
	wikiLang       *regexp.Regexp = regexp.MustCompile(`(\s==|^==)[\w\s]+==`)          // most languages are a single word; there are some that are multiple words
	wikiLexM       *regexp.Regexp = regexp.MustCompile(`(\s====|^====)[\w\s]+====`)    // lexical category could be multi-word (e.g. "Proper Noun") match for multi-etymology
	wikiLexS       *regexp.Regexp = regexp.MustCompile(`(\s===|^===)[\w\s]+===`)       // lexical category match for single etymology
	wikiEtymologyS *regexp.Regexp = regexp.MustCompile(`(\s===|^===)Etymology===`)     // check for singular etymology
	wikiEtymologyM *regexp.Regexp = regexp.MustCompile(`(\s===|^===)Etymology \d+===`) // these heading may or may not have a number designation
	wikiNumListAny *regexp.Regexp = regexp.MustCompile(`\s##?[\*:]*? `)                // used to find all num list indices
	wikiNumList    *regexp.Regexp = regexp.MustCompile(`\s#[^:\*] `)                   // used to find the num list entries that are of concern
	wikiGenHeading *regexp.Regexp = regexp.MustCompile(`(\s=+|^=+)[\w\s]+`)            // generic heading search
	wikiNewLine    *regexp.Regexp = regexp.MustCompile(`\n`)
	wikiBracket    *regexp.Regexp = regexp.MustCompile(`[\[\]]+`)
	wikiWordAlt    *regexp.Regexp = regexp.MustCompile(`\[\[([\w\s]+)\|[\w\s]+\]\]`)
	wikiModifier   *regexp.Regexp = regexp.MustCompile(`\{\{m\|\w+\|([\w\s]+)\}\}`)
	wikiLabel      *regexp.Regexp = regexp.MustCompile(`\{\{(la?b?e?l?)\|\w+\|([\w\s\|'",;\(\)_\[\]-]+)\}\}`)
	wikiTplt       *regexp.Regexp = regexp.MustCompile(`\{\{|\}\}`) // open close template bounds "{{ ... }}"
	wikiExample    *regexp.Regexp = regexp.MustCompile(`\{\{examples(.+)\}\}`)
	//wikiRefs       *regexp.Regexp = regexp.MustCompile(`\<ref\>(.*?)\</ref\>`)
	htmlBreak *regexp.Regexp = regexp.MustCompile(`\<br\>`)

	// other stuff
	language        string             = ""
	logger          *colorlog.ColorLog = &colorlog.ColorLog{}
	lexicalCategory []string           = []string{"Proper noun", "Noun", "Adjective", "Adverb",
		"Verb", "Article", "Particle", "Conjunction",
		"Pronoun", "Determiner", "Interjection", "Morpheme",
		"Numeral", "Preposition", "Postposition"}
)

type WikiData struct {
	XMLName xml.Name `xml:"mediawiki"`
	Pages   []Page   `xml:"page"`
}

type Page struct {
	XMLName   xml.Name   `xml:"page"`
	Title     string     `xml:"title"`
	Id        int        `xml:"id"`
	Revisions []Revision `xml:"revision"`
}

type Revision struct {
	Id      int    `xml:"id"`
	Comment string `xml:"comment"`
	Model   string `xml:"model"`
	Format  string `xml:"format"`
	Text    string `xml:"text"`
	Sha1    string `xml:"sha1"`
}

type Insert struct {
	Word      string
	Etymology int
	CatDefs   map[string][]string
}

func main() {
	iFile := flag.String("file", "", "XML file to parse")
	db := flag.String("database", "database.db", "Database file to use")
	lang := flag.String("lang", "English", "Language to target for parsing")
	cacheFile := flag.String("cache_file", "xmlCache.gob", "Use this as the cache file")
	logFile := flag.String("log_file", "", "Log to this file")
	threads := flag.Int("threads", 5, "Number of threads to use for parsing")
	useCache := flag.Bool("use_cache", false, "Use a 'gob' of the parsed XML file")
	makeCache := flag.Bool("make_cache", false, "Make a cache file of the parsed XML")
	purge := flag.Bool("purge", false, "Purge the selected database")
	verbose := flag.Bool("verbose", false, "Use verbose logging")
	flag.Parse()

	if *logFile != "" {
		logger = colorlog.NewFileLog(colorlog.Linfo, *logFile)
	} else {
		logger = colorlog.New(colorlog.Linfo)
	}

	if *verbose {
		logger.SetLogLevel(colorlog.Ldebug)
	}

	language = *lang

	start_time := time.Now()
	logger.Info("+--------------------------------------------------\n")
	logger.Info("| Start Time    :    %v\n", start_time)
	logger.Info("| Parse File    :    %s\n", *iFile)
	logger.Info("| Database      :    %s\n", *db)
	logger.Info("| Language      :    %s\n", language)
	logger.Info("| Cache File    :    %s\n", *cacheFile)
	logger.Info("| Use Cache     :    %t\n", *useCache)
	logger.Info("| Make Cache    :    %t\n", *makeCache)
	logger.Info("| Verbose       :    %t\n", *verbose)
	logger.Info("| Purge         :    %t\n", *purge)
	logger.Info("+--------------------------------------------------\n")

	logger.Debug("NOTE: input language should be provided as a proper noun. (e.g. English, French, West Frisian, etc.)\n")

	data := &WikiData{}
	if *useCache {
		d, err := decodeCache(*cacheFile)
		data = d
		check(err)
	} else if *iFile == "" {
		logger.Error("Input file is empty. Exiting\n")
		os.Exit(1)
	} else {
		logger.Info("Parsing XML file\n")
		d := parseXML(*makeCache, *iFile, *cacheFile)
		data = d
	}

	if *purge {
		err := os.Remove(*db)
		check(err)
	}

	logger.Debug("Number of Pages: %d\n", len(data.Pages))
	logger.Info("Opening database\n")
	dbh, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?cache=shared&mode=rwc&_mutex=full&_busy_timeout=500", *db))
	check(err)
	dbh.SetMaxOpenConns(1)

	sth, err := dbh.Prepare(`CREATE TABLE IF NOT EXISTS dictionary
                             (
                                 id INTEGER PRIMARY KEY,
                                 word TEXT,
                                 lexical_category TEXT,
                                 etymology_no INTEGER,
                                 definition_no INTEGER,
                                 definition TEXT
                             )`)
	check(err)
	sth.Exec()

	sth, err = dbh.Prepare(`CREATE INDEX IF NOT EXISTS dict_word_idx
                            ON dictionary (word, lexical_category, etymology_no, definition_no)`)

	check(err)
	sth.Exec()

	filterPages(data)
	logger.Info("Post filter page count: %d\n", len(data.Pages))

	// split the work into 5 chunks
	var chunks [][]Page
	size := len(data.Pages) / *threads
	logger.Debug("Chunk size: %d\n", size)
	logger.Debug(" >> %d\n", len(data.Pages)/size)
	for i := 0; i < *threads; i++ {
		end := size + size*i
		if end > len(data.Pages) || i+1 == *threads {
			end = len(data.Pages)
		}
		logger.Debug("Splitting chunk %d :: [%d, %d]\n", i, size*i, end)
		chunks = append(chunks, data.Pages[size*i:end])
	}

	logger.Debug("Have %d chunks\n", len(chunks))
	logger.Debug("Chunk Page Last: %s Page Last: %s\n", chunks[len(chunks)-1][len(chunks[len(chunks)-1])-1].Title, data.Pages[len(data.Pages)-1].Title)

	var wg sync.WaitGroup
	for i := 0; i < *threads; i++ {
		wg.Add(1)
		go pageWorker(i, &wg, chunks[i], dbh)
	}

	wg.Wait()

	end_time := time.Now()
	logger.Info("Completed in %s\n", end_time.Sub(start_time))
}

func pageWorker(id int, wg *sync.WaitGroup, pages []Page, dbh *sql.DB) {
	defer wg.Done()
	inserts := []*Insert{} // etymology : lexical category : [definitions...]
	for _, page := range pages {
		word := page.Title
		logger.Debug("Processing page: %s\n", word)

		// convert the text to a byte string
		text := []byte(page.Revisions[0].Text)
		logger.Debug("Raw size: %d\n", len(text))

		text = wikiModifier.ReplaceAll(text, []byte("'$1'"))
		logger.Debug("Modifier size: %d\n", len(text))

		//text = wikiLabel.ReplaceAll(text, []byte("(${2})"))
		//logger.Debug("Label size: %d\n", len(text))

		text = wikiExample.ReplaceAll(text, []byte(""))
		logger.Debug("Example size: %d\n", len(text))

		text = wikiWordAlt.ReplaceAll(text, []byte("$1"))
		logger.Debug("WordAlt size: %d\n", len(text))

		text = wikiBracket.ReplaceAll(text, []byte(""))
		logger.Debug("Bracket size: %d\n", len(text))

		text = htmlBreak.ReplaceAll(text, []byte(" "))
		logger.Debug("Html Break size: %d\n", len(text))

		text_size := len(text)
		logger.Debug("Starting Size of corpus: %d bytes\n", text_size)

		// get language section of the page
		text = getLanguageSection(text)
		logger.Debug("Reduced corpus by %d bytes to %d\n", text_size-len(text), len(text))

		// get all indices of the etymology headings
		etymology_idx := wikiEtymologyM.FindAllIndex(text, -1)
		if len(etymology_idx) == 0 {
			logger.Debug("Did not find multi-style etymology. Checking for singular ...\n")
			etymology_idx = wikiEtymologyS.FindAllIndex(text, -1)
		}
		/*
		   When there is only a single or no etymology, then lexical catetories are of the form ===[\w\s]+===
		   Otherwise, then lexical catigories are of the form ====[\w\s]+====
		*/
		logger.Debug("Found %d etymologies\n", len(etymology_idx))
		if len(etymology_idx) <= 1 {
			// need to get the lexical category via regexp
			logger.Debug("Parsing by lexical category\n")
			lexcat_idx := wikiLexS.FindAllIndex(text, -1)
			inserts = append(inserts, parseByLexicalCategory(word, lexcat_idx, text)...)
		} else {
			logger.Debug("Parsing by etymologies\n")
			inserts = append(inserts, parseByEtymologies(word, etymology_idx, text)...)
		}
	}

	// perform inserts
	inserted := performInserts(dbh, inserts)
	logger.Info("[%2d] Inserted %6d records for %6d pages\n", id, inserted, len(pages))
}

func performInserts(dbh *sql.DB, inserts []*Insert) int {
	ins_count := 0
	query := `INSERT INTO dictionary (word, lexical_category, etymology_no, definition_no, definition)
              VALUES (?, ?, ?, ?, ?)`

	logger.Debug("performInserts> Preparing insert query...\n")
	tx, err := dbh.Begin()
	check(err)
	defer tx.Rollback()

	sth, err := tx.Prepare(query)
	check(err)
	defer sth.Close()

	for _, ins := range inserts {
		logger.Debug("performInserts> et_no=>'%d' defs=>'%+v'\n", ins.Etymology, ins.CatDefs)
		for key, val := range ins.CatDefs {
			category := key
			for def_no, def := range val {
				logger.Debug("performInserts> Inserting values: word=>'%s', lexical category=>'%s', et_no=>'%d', def_no=>'%d', def=>'%s'\n",
					ins.Word, category, ins.Etymology, def_no, def)
				_, err := sth.Exec(ins.Word, category, ins.Etymology, def_no, def)
				check(err)
				ins_count++
			}
		}
	}

	err = tx.Commit()
	check(err)

	return ins_count
}

func parseByEtymologies(word string, et_list [][]int, text []byte) []*Insert {
	inserts := []*Insert{}
	et_size := len(et_list)
	for i := 0; i < et_size; i++ {
		ins := &Insert{Word: word, Etymology: i, CatDefs: make(map[string][]string)}
		section := []byte{}
		if i+1 >= et_size {
			section = getSection(et_list[i][1], -1, text)
		} else {
			section = getSection(et_list[i][1], et_list[i+1][0], text)
		}

		logger.Debug("parseByEtymologies> Section is %d bytes\n", len(section))

		lexcat_idx := wikiLexM.FindAllIndex(section, -1)
		lexcat_idx_size := len(lexcat_idx)

		definitions := []string{}
		for j := 0; j < lexcat_idx_size; j++ {
			jth_idx := adjustIndexLW(lexcat_idx[j][0], section)
			lexcat := string(section[jth_idx+4 : lexcat_idx[j][1]-4])
			logger.Debug("parseByEtymologies> [%2d] lexcat: %s\n", j, lexcat)

			if !stringInSlice(lexcat, lexicalCategory) {
				logger.Debug("parseByLemmas> Lexical category '%s' not in list. Skipping...\n", lexcat)
				continue
			}

			nHeading := wikiGenHeading.FindIndex(section[lexcat_idx[j][1]:])
			if len(nHeading) > 0 {
				nHeading[0] = nHeading[0] + lexcat_idx[j][1]
				nHeading[1] = nHeading[1] + lexcat_idx[j][1]
				logger.Debug("parseByLemmas> LEM_LIST %d: %+v NHEADING: %+v\n", j, lexcat_idx[j], nHeading)
				definitions = getDefinitions(lexcat_idx[j][1], nHeading[0], section)
			} else if j+1 >= lexcat_idx_size {
				definitions = getDefinitions(lexcat_idx[j][1], -1, section)
			} else {
				jth_1_idx := adjustIndexLW(lexcat_idx[j+1][0], section)
				definitions = getDefinitions(lexcat_idx[j][1], jth_1_idx, section)
			}
			logger.Debug("parseByEtymologies> Definitions: " + strings.Join(definitions, ", ") + "\n")
			ins.CatDefs[lexcat] = definitions
		}
		inserts = append(inserts, ins)
	}

	return inserts
}

//parseByLemmas
func parseByLexicalCategory(word string, lex_list [][]int, text []byte) []*Insert {
	inserts := []*Insert{}
	lex_size := len(lex_list)
	logger.Debug("parseByLexicalCategory> Found %d lexcats\n", lex_size)

	for i := 0; i < lex_size; i++ {
		ins := &Insert{Word: word, Etymology: 0, CatDefs: make(map[string][]string)}
		ith_idx := adjustIndexLW(lex_list[i][0], text)
		lexcat := string(text[ith_idx+3 : lex_list[i][1]-3])

		logger.Debug("parseByLexicalCategory> [%2d] working on lexcat '%s'\n", i, lexcat)

		if !stringInSlice(lexcat, lexicalCategory) {
			logger.Debug("parseByLexicalCategory> Lemma '%s' not in list. Skipping...\n", lexcat)
			continue
		}

		definitions := []string{}
		if i+1 >= lex_size {
			definitions = getDefinitions(lex_list[i][1], -1, text)
		} else {
			ith_1_idx := adjustIndexLW(lex_list[i+1][0], text)
			logger.Debug("parseByLexicalCategory> LEMMA: %s\n", string(text[lex_list[i][1]:ith_1_idx]))
			definitions = getDefinitions(lex_list[i][1], ith_1_idx, text)
		}

		logger.Debug("parseByLexicalCategory> Found %d definitions\n", len(definitions))
		ins.CatDefs[lexcat] = definitions

		inserts = append(inserts, ins)
	}

	return inserts
}

func getDefinitions(start int, end int, text []byte) []string {
	category := []byte{}
	defs := []string{}

	if end < 0 {
		category = text[start:]
	} else {
		category = text[start:end]
	}

	logger.Debug("getDefinitions> TEXT: %s\n", string(text))
	nHeading := wikiGenHeading.FindIndex(text[start:])
	logger.Debug("getDefinitions> START: %d END: %d NHEADING: %+v\n", start, end, nHeading)
	if len(nHeading) > 0 && nHeading[1]+start < end {
		nHeading[0], nHeading[1] = nHeading[0]+start, nHeading[1]+start
		category = text[start:nHeading[0]]
	}

	nl_indices := wikiNumListAny.FindAllIndex(category, -1)
	logger.Debug("getDefinitions> Found %d NumList entries\n", len(nl_indices))
	nl_indices_size := len(nl_indices)
	for i := 0; i < nl_indices_size; i++ {
		ith_idx := adjustIndexLW(nl_indices[i][0], category)
		if string(category[ith_idx:nl_indices[i][1]]) != "# " {
			logger.Debug("getDefinitions> Got quotation or annotation bullet. Skipping...\n")
			continue
		}

		if i+1 >= nl_indices_size && string(category[ith_idx:nl_indices[i][1]]) == "# " {
			def := parseDefinition(nl_indices[i][1], len(category), category)
			logger.Debug("getDefinitions> [%0d] Appending %s to the definition list\n", i, string(def))
			defs = append(defs, string(def))
		}

		if i+1 < nl_indices_size && string(category[ith_idx:nl_indices[i][1]]) == "# " {
			ith_1_idx := adjustIndexLW(nl_indices[i+1][0], category)
			def := parseDefinition(nl_indices[i][1], ith_1_idx, category)
			logger.Debug("getDefinitions> [%0d] Appending %s to the definition list\n", i, string(def))
			defs = append(defs, string(def))
		}
	}

	logger.Debug("getDefinitions> Got %d definitions\n", len(defs))
	return defs
}

func parseDefinition(start int, end int, text []byte) []byte {
	def := text[start:end]
	//def = wikiNewLine.ReplaceAll(def, []byte(" "))

	// need to parse the templates in the definition
	sDef, err := wikitemplates.ParseRecursive(def)
	check(err)

	def = []byte(sDef)
	newline := wikiNewLine.FindIndex(def)

	if len(newline) > 0 {
		def = def[:newline[0]]
	}

	def = bytes.TrimSpace(def)

	return def
}

func getLanguageSection(text []byte) []byte {
	// this is going to pull out the "section" of the text bounded by the
	// desired language heading and the following heading or the end of
	// the data.

	indices := wikiLang.FindAllIndex(text, -1)
	indices_size := len(indices)

	logger.Debug("CORPUS: %s\n", string(text))
	logger.Debug("CORPUS SIZE: %d INDICES_SIZE: %d INDICES: %+v\n", len(text), indices_size, indices)

	if indices_size == 0 {
		return text
	}

	// when the match has a leading \s, remove it
	if text[indices[0][0] : indices[0][0]+1][0] == byte('\n') {
		indices[0][0]++
	}

	if indices_size == 1 {
		// it is assumed at this point that the pages have been filterd by the
		// desired language already, which means that the only heading present
		// is the one that is wanted.
		logger.Debug("Found only 1 heading. Returning corpus for heading '%s'\n", string(text[indices[0][0]:indices[0][1]]))
		return text[indices[0][1]:]
	}

	logger.Debug("Found %d indices\n", indices_size)
	logger.Debug("Indices: %v\n", indices)
	corpus := text
	for i := 0; i < indices_size; i++ {
		heading := string(text[indices[i][0]:indices[i][1]])
		logger.Debug("Checking heading: %s\n", heading)

		if heading != fmt.Sprintf("==%s==", language) {
			logger.Debug("'%s' != '==%s=='\n", heading, language)
			continue
		}

		if i == indices_size-1 {
			logger.Debug("Found last heading\n")
			return text[indices[i][1]:]
		}

		corpus = text[indices[i][1]:indices[i+1][0]]
		break
	}

	return corpus
}

// filter out the pages that are not words in the desired language
func filterPages(wikidata *WikiData) {
	engCheck := regexp.MustCompile(fmt.Sprintf(`==%s==`, language))
	spaceCheck := regexp.MustCompile(`[:0-9]`)
	skipCount := 0
	i := 0
	for i < len(wikidata.Pages) {
		if !engCheck.MatchString(wikidata.Pages[i].Revisions[0].Text) || spaceCheck.MatchString(wikidata.Pages[i].Title) {
			// remove the entry from the array
			wikidata.Pages[i] = wikidata.Pages[len(wikidata.Pages)-1]
			wikidata.Pages = wikidata.Pages[:len(wikidata.Pages)-1]
			skipCount++
			continue
		}
		i++
	}

	logger.Debug("Skipped %d pages\n", skipCount)
}

// parse the input XML file into a struct and create a cache file optionally
func parseXML(makeCache bool, parseFile string, cacheFile string) *WikiData {
	logger.Info("Opening xml file\n")
	file, err := ioutil.ReadFile(parseFile)
	check(err)

	wikidata := &WikiData{}

	start := time.Now()
	logger.Info("Unmarshalling xml ... ")
	err = xml.Unmarshal(file, wikidata)
	end := time.Now()
	logger.Printc(colorlog.Linfo, colorlog.Grey, "elapsed %s\n", end.Sub(start))
	check(err)

	logger.Info("Parsed %d pages\n", len(wikidata.Pages))

	if makeCache {
		err = encodeCache(wikidata, cacheFile)
		check(err)
	}

	return wikidata
}

// encode the data into a binary cache file
func encodeCache(data *WikiData, file string) error {
	logger.Info("Creating binary cache: '%s'\n", file)
	cacheFile, err := os.Create(file)
	if err != nil {
		return err
	}

	enc := gob.NewEncoder(cacheFile)

	start := time.Now()
	logger.Debug("Encoding data ... ")
	enc.Encode(data)
	end := time.Now()
	logger.Printc(colorlog.Ldebug, colorlog.Green, "elapsed %s\n", end.Sub(start))

	logger.Info("Binary cache built.\n")
	cacheFile.Close()

	return nil
}

// decode binary cache file into a usable struct
func decodeCache(file string) (*WikiData, error) {
	logger.Info("Initializing cached object\n")
	cacheFile, err := os.Open(file)
	if err != nil {
		return nil, err
	}

	data := &WikiData{}
	dec := gob.NewDecoder(cacheFile)

	start := time.Now()
	logger.Debug("Decoding data ... ")
	dec.Decode(data)
	end := time.Now()
	logger.Printc(colorlog.Ldebug, colorlog.Green, "elapsed %s\n", end.Sub(start))

	logger.Info("Cache initialized.\n")
	cacheFile.Close()

	return data, nil
}

// Helper functions
func check(err error) {
	if err != nil {
		logger.Fatal("%s\n", err.Error())
		panic(err)
	}
}

func getSection(start int, end int, text []byte) []byte {
	if end < 0 {
		return text[start:]
	}

	return text[start:end]
}

func stringInSlice(str string, list []string) bool {
	for _, lStr := range list {
		if str == lStr {
			return true
		}
	}
	return false
}

// adjust the index offset to account for leading whitespace character
func adjustIndexLW(index int, text []byte) int {
	if text[index : index+1][0] == byte('\n') {
		index++
	}
	return index
}
