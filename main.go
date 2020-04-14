package main

import (
	"database/sql"
	"encoding/gob"
	"encoding/xml"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/macdub/go-colorlog"
	_ "github.com/mattn/go-sqlite3"
)

var (
	wikiLang       *regexp.Regexp     = regexp.MustCompile(`(\s==|^==)[\w\s]+==`)          // most languages are a single word; there are some that are multiple words
	wikiLemmaM     *regexp.Regexp     = regexp.MustCompile(`(\s====|^====)[\w\s]+====`)    // lemmas could be multi-word (e.g. "Proper Noun") match for multi-etymology
	wikiLemmaS     *regexp.Regexp     = regexp.MustCompile(`(\s===|^===)[\w\s]+===`)       // lemma match for single etymology
	wikiEtymologyS *regexp.Regexp     = regexp.MustCompile(`(\s===|^===)Etymology===`)     // check for singular etymology
	wikiEtymologyM *regexp.Regexp     = regexp.MustCompile(`(\s===|^===)Etymology \d+===`) // these heading may or may not have a number designation
	wikiNumListAny *regexp.Regexp     = regexp.MustCompile(`\s?#[\*:]? `)                  // used to find all num list indices
	wikiNumList    *regexp.Regexp     = regexp.MustCompile(`\s?#[^:\*] `)                  // used to find the num list entries that are of concern
	wikiGenHeading *regexp.Regexp     = regexp.MustCompile(`(\s=+|^=+)[\w\s]+`)            // generic heading search
	wikiNewLine    *regexp.Regexp     = regexp.MustCompile(`\n`)
	language       string             = ""
	logger         *colorlog.ColorLog = &colorlog.ColorLog{}
	lemmaList      []string           = []string{"Proper noun", "Noun", "Adjective", "Adverb",
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
	Etymology int
	LemmaDefs map[string][]string
}

func main() {
	iFile := flag.String("file", "", "XML file to parse")
	db := flag.String("database", "database.db", "Database file to use")
	lang := flag.String("lang", "English", "Language to target for parsing")
	cacheFile := flag.String("cache_file", "xmlCache.gob", "Use this as the cache file")
	logFile := flag.String("log_file", "", "Log to this file")
	useCache := flag.Bool("use_cache", false, "Use a 'gob' of the parsed XML file")
	makeCache := flag.Bool("make_cache", false, "Make a cache file of the parsed XML")
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

	logger.Debug("Number of Pages: %d\n", len(data.Pages))
	logger.Info("Opening database\n")
	dbh, err := sql.Open("sqlite3", *db)
	check(err)

	sth, err := dbh.Prepare(`CREATE TABLE IF NOT EXISTS dictionary
                             (
                                 id INTEGER PRIMARY KEY,
                                 word TEXT,
                                 lemma TEXT,
                                 etymology_no INTEGER,
                                 definition_no INTEGER,
                                 definition TEXT
                             )`)
	check(err)
	sth.Exec()

	sth, err = dbh.Prepare(`CREATE INDEX IF NOT EXISTS dict_word_idx
                            ON dictionary (word, lemma, etymology_no, definition_no)`)

	check(err)
	sth.Exec()

	filterPages(data)
	logger.Info("Post filter page count: %d\n", len(data.Pages))

	// next step to work through each page and parse out the lemmas and definitions
	// count := 30
	for _, page := range data.Pages {
		//if count <= 0 {
		//	break
		//}
		//count--

		word := page.Title
		inserts := []*Insert{} // etymology : lemma : [definitions...]
		logger.Debug("-----START " + word + "-----\n")
		logger.Info("Processing page: %s\n", word)

		// convert the text to a byte string
		text := []byte(page.Revisions[0].Text)
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
		   When there is only a single or no etymology, then lemmas are of the form ===[\w\s]+===
		   Otherwise, then lemmas are of the form ====[\w\s]+====
		*/
		logger.Debug("Found %d etymologies\n", len(etymology_idx))
		if len(etymology_idx) <= 1 {
			// need to get the lemmas via regexp
			logger.Debug("Parsing by lemmas\n")
			lemma_idx := wikiLemmaS.FindAllIndex(text, -1)
			inserts = parseByLemmas(lemma_idx, text)
		} else {
			logger.Debug("Parsing by etymologies\n")
			inserts = parseByEtymologies(etymology_idx, text)
		}

		logger.Debug("Definition map:\n%+v\n", inserts)
		logger.Debug("-----END " + word + "-----\n")

		// perform inserts
		inserted := performInserts(dbh, word, inserts)
		logger.Info("Inserted %d records\n", inserted)
	}

    end_time := time.Now()
    logger.Info("Completed in %s\n", end_time.Sub(start_time))
}

func performInserts(dbh *sql.DB, word string, inserts []*Insert) int {
	ins_count := 0
	query := `INSERT INTO dictionary (word, lemma, etymology_no, definition_no, definition)
              VALUES (?, ?, ?, ?, ?)`

	logger.Debug("performInserts> Preparing insert query...\n")
	tx, err := dbh.Begin()
	check(err)
    defer tx.Rollback()

    sth, err := tx.Prepare(query)
    check(err)
    defer sth.Close()

	for _, ins := range inserts {
		et_no := ins.Etymology
		defs := ins.LemmaDefs

        logger.Debug("performInserts> et_no=>'%d' defs=>'%+v'\n", et_no, defs)
		for key, val := range defs {
			lemma := key
			for i, def := range val {
				def_no := i
				logger.Debug("performInserts> Inserting values: word=>'%s', lemma=>'%s', et_no=>'%d', def_no=>'%d', def=>'%s'\n",
					word, lemma, et_no, def_no, def)
                _, err :=sth.Exec(word, lemma, et_no, def_no, def)
                check(err)
				ins_count++
			}
		}
	}

    err = tx.Commit()
    check(err)

	return ins_count
}

func parseByEtymologies(et_list [][]int, text []byte) []*Insert {
	inserts := []*Insert{}
	et_size := len(et_list)
	for i := 0; i < et_size; i++ {
		ins := &Insert{Etymology: i, LemmaDefs: make(map[string][]string)}
		section := []byte{}
		if i+1 >= et_size {
			section = getSection(et_list[i][1], -1, text)
		} else {
			section = getSection(et_list[i][1], et_list[i+1][0], text)
		}

		logger.Debug("parseByEtymologies> Section is %d bytes\n", len(section))

		lemma_idx := wikiLemmaM.FindAllIndex(section, -1)
		lemma_idx_size := len(lemma_idx)

		definitions := []string{}
		for j := 0; j < lemma_idx_size; j++ {
			jth_idx := adjustIndexLW(lemma_idx[j][0], section)
			lemma := string(section[jth_idx+4 : lemma_idx[j][1]-4])
			logger.Debug("parseByEtymologies> [%2d] lemma: %s\n", j, lemma)

            if !stringInSlice(lemma, lemmaList) {
                logger.Debug("parseByLemmas> Lemma '%s' not in list. Skipping...\n", lemma)
                continue
            }

			if j+1 >= lemma_idx_size {
				definitions = getDefinitions(lemma_idx[j][1], -1, section)
			} else {
				jth_1_idx := adjustIndexLW(lemma_idx[j+1][0], section)
				definitions = getDefinitions(lemma_idx[j][1], jth_1_idx, section)
			}
			logger.Debug("parseByEtymologies> Definitions: " + strings.Join(definitions, ", ") + "\n")
			ins.LemmaDefs[lemma] = definitions
		}
		inserts = append(inserts, ins)
	}

	return inserts
}

func parseByLemmas(lem_list [][]int, text []byte) []*Insert {
	inserts := []*Insert{}
	lem_size := len(lem_list)
	logger.Debug("parseByLemmas> Found %d lemmas\n", lem_size)

	for i := 0; i < lem_size; i++ {
		ins := &Insert{Etymology: 0, LemmaDefs: make(map[string][]string)}
		ith_idx := adjustIndexLW(lem_list[i][0], text)
		lemma := string(text[ith_idx+3 : lem_list[i][1]-3])

		logger.Debug("parseByLemmas> [%2d] working on lemma '%s'\n", i, lemma)

		if !stringInSlice(lemma, lemmaList) {
			logger.Debug("parseByLemmas> Lemma '%s' not in list. Skipping...\n", lemma)
			continue
		}

		definitions := []string{}
		if i+1 >= lem_size {
			definitions = getDefinitions(lem_list[i][1], -1, text)
		} else {
			ith_1_idx := adjustIndexLW(lem_list[i+1][0], text)
			definitions = getDefinitions(lem_list[i][1], ith_1_idx, text)
		}

		logger.Debug("parseByLemmas> Found %d definitions\n", len(definitions))
        ins.LemmaDefs[lemma] = definitions

		inserts = append(inserts, ins)
	}

	return inserts
}

func getDefinitions(start int, end int, text []byte) []string {
	lemma_sect := []byte{}
	defs := []string{}

	if end < 0 {
		lemma_sect = text[start:]
	} else {
		lemma_sect = text[start:end]
	}

	nl_indices := wikiNumListAny.FindAllIndex(lemma_sect, -1)
	logger.Debug("getDefinitions> Found %d NumList entries\n", len(nl_indices))
	nl_indices_size := len(nl_indices)
	for i := 0; i < nl_indices_size; i++ {
		ith_idx := adjustIndexLW(nl_indices[i][0], lemma_sect)
		if string(lemma_sect[ith_idx:nl_indices[i][1]]) != "# " {
			logger.Debug("getDefinitions> Got quotation or annotation bullet. Skipping...\n")
			continue
		}

		logger.Debug("getDefinitions> Checks: (i+1 >= nl_indices_size)=>%5t (lemma_sect[ith_idx:nl_indices[i][1]] == '# ')=>%t\n",
			(i+1 >= nl_indices_size), (string(lemma_sect[ith_idx:nl_indices[i][1]]) == "# "))
		if i+1 >= nl_indices_size && string(lemma_sect[ith_idx:nl_indices[i][1]]) == "# " {
			def := lemma_sect[nl_indices[i][1]:]
			idx := wikiNewLine.FindIndex(def)
			logger.Debug("getDefinitions> Sizeof(def)=>%d range=>[%d, %d)\n", len(def), nl_indices[i][1], len(lemma_sect))
			logger.Debug("getDefinitions> [%0d] NewLine index: %v\n", i, idx)
			if idx != nil {
				def = def[:idx[0]]
			}
			logger.Debug("getDefinitions> [%0d] Appending %s to the definition list\n", i, string(def))
			defs = append(defs, string(def))
		}

		logger.Debug("getDefinitions> Checks: (i+1 <  nl_indices_size)=>%5t (lemma_sect[ith_idx:nl_indices[i][1]] == '# ')=>%t\n",
			(i+1 < nl_indices_size), (string(lemma_sect[ith_idx:nl_indices[i][1]]) == "# "))
		if i+1 < nl_indices_size && string(lemma_sect[ith_idx:nl_indices[i][1]]) == "# " {
			ith_1_idx := adjustIndexLW(nl_indices[i+1][0], lemma_sect)
			def := lemma_sect[nl_indices[i][1]:ith_1_idx]
			idx := wikiNewLine.FindIndex(def)
			logger.Debug("getDefinitions> Sizeof(def)=>%d range=>[%d, %d)\n", len(def), nl_indices[i][1], len(lemma_sect))
			logger.Debug("getDefinitions> [%0d] NewLine index (i:i+1): %v\n", i, idx)
			if idx != nil {
				def = def[:idx[0]]
			}
			logger.Debug("getDefinitions> [%0d] Appending %s to the definition list\n", i, string(def))
			defs = append(defs, string(def))
		}
	}

	logger.Debug("getDefinitions> Got %d definitions\n", len(defs))
	return defs
}

func getLanguageSection(text []byte) []byte {
	// this is going to pull out the "section" of the text bounded by the
	// desired language heading and the following heading or the end of
	// the data.

	indices := wikiLang.FindAllIndex(text, -1)
	indices_size := len(indices)

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
	spaceCheck := regexp.MustCompile(`[-:\s0-9]`)
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
