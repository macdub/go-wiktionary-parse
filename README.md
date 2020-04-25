# go-wiktionary-parse
This is a tool to parse language dumps from Wiktionary and store the results into a Sqlite database.

## Usage
```
Usage of wiktionary-parser:
    -cache_file string
        Use this as the cache file (default "xmlCache.gob")
    -database string
        Database file to use (default "database.db")
    -file string
        XML file to parse
    -lang string
        Language to target for parsing (default "English")
    -log_file string
        Log to this file
    -make_cache
        Make a cache file of the parsed XML
    -threads int
        Set the number of threads to use for parsing (default 5)
    -use_cache
        Use a 'gob' of the parsed XML file
    -verbose
        Use verbose logging
```

## Build
### Dependencies
- ColorLog: https://github.com/macdub/go-colorlog
- Sqlite3: https://github.com/mattn/go-sqlite3

### Build
`$ go build -o wiktionary-parser main.go`

## Current Limitations
- Only parses single words. No entries with ' ', '-', ':' in the page title (e.g. Wiktionary:Main\_Page).
- It only looks at 14 lemmas
- Does not clean the definition. Meaning it looks like raw wiki markup. This is something that will be fixed in the near future.

## Database
- [Pre-Built Databases](http://www.mcdojoh.com/wiktionary_dbs)

### Structure
- table name: dictionary

| COLUMN         | TYPE    |
|:--------------:|:-------:|
| id             | integer |
| word           | text    |
| lemma          | text    |
| etymology\_no  | integer | 
| definition\_no | integer |
| definition     | text    |

- Primary key is on ID
- Index is setup over word, lemma, etymology\_no, definition\_no

### Statistics
- The wiktionary dump as of 20200401 that is only the current version is ~3.3GB
- The database (20200416) file that is built is ~98MB
  - 717,438 words
  - 941,713 definitions
  - 14 lemmas
