package wikitemplates

import (
    "regexp"
    "strings"
    "fmt"
    //"errors"

    "github.com/golang-collections/collections/stack"
)

var (
    wikiTplt  *regexp.Regexp = regexp.MustCompile(`\{\{|\}\}`) // open close template bounds "{{ ... }}"
    wikiOpen  *regexp.Regexp = regexp.MustCompile(`\{`)
    wikiClose *regexp.Regexp = regexp.MustCompile(`\}`)
    htmlMath  *regexp.Regexp = regexp.MustCompile(`\<math\>(.+)?\</math\>`)
    NumParam  *regexp.Regexp = regexp.MustCompile(`[0-9]+`)
    wikiSubs []string = []string{"q", "l", "lb", "lbl", "label"}
    langList []string = []string{"en", "mul", "de", "fr", "it", "es"}
    tags = map[string]string {
        "abbreviation of" : "abbreviation of ",
        "abbr of" : "abbreviation of ",
        "plural of": "plural of ",
        "en-third-person singular of": "3rd person singular form of ",
        "non-gloss definition": "",
        "gloss": "",
        "alternative form of": "alternative form of ",
        "alt form": "alternative form of ",
        "surname": "surname",
        "given name": "given name ",
        "place": "place: ",
        "en-past of": "past participle of ",
        "present participle of": "present participle of ",
        "synonym of": "synonym of ",
        "initialism of": "initialism of ",
        "init of": "initialism of ",
        "nonstandard spelling of": "nonstandard spelling of ",
        "q": "\"%s\"",
        "l": "(%s)",
        "lb": "(%s)",
        "lbl": "(%s)",
        "label": "(%s)",
        "misspelling of": "misspelling of ",
        "defdate": "",
        "w": "",
        "cln": "",
        "obsolete form of": "obsolete form of ",
        "1": "",
    }
)

type WiktionaryTemplate struct {
    /*
        {{<template name>|anon_param1|anon_param2|...}}
        {{<template name>|1=num_param1|2=num_param2|...}}
        {{<template name>|np1=name_param1|...}}

        - mixing parameter type is acceptable
          {{alt form|en|worth|t=to become}}
    */
    TemplateName string
    Params []string
    PrettyStr string
    Start int
    End int
}

func ParseRecursive(data []byte) (string, error) {
    math_indices := htmlMath.FindAllIndex(data, -1)

    if len(math_indices) > 0 {
        math_chunk := data[math_indices[0][0]:math_indices[0][1]]
        math_chunk = wikiOpen.ReplaceAll(math_chunk, []byte("("))
        math_chunk = wikiClose.ReplaceAll(math_chunk, []byte(")"))
        data = htmlMath.ReplaceAll(data, math_chunk)
    }

    // get the indices of open/close pairs
    indices := wikiTplt.FindAllIndex(data, -1)

    if len(indices) == 0 || len(indices) % 2 != 0 {
        return string(data), nil
    }

    /*fmt.Printf("DATA SIZE: %d\n", len(data))
    fmt.Printf("START OF 1ST INDEX: %d\n", indices[0][0])
    fmt.Printf("END OF LAST INDEX: %d\n", indices[len(indices)-1][1])*/
    var templates []*WiktionaryTemplate
    stack := stack.New()
    var nested []*WiktionaryTemplate
    //fmt.Printf("ParseRecursive> DATA: %s\n", string(data))
    for i := 0; i < len(indices); i++ {
        chunk := data[indices[i][0]:indices[i][1]]
        if string(chunk) == "{{" {
            stack.Push(chunk)
            stack.Push(data[indices[i][1]:indices[i+1][0]])
        }

        if string(chunk) == "}}" && stack.Len() > 0 {
            val := string(stack.Pop().([]byte))
            for stack.Len() > 0 {
                if val != "{{" {
                    tmplt := ParseWiktionaryTemplate(val)
                    nested = append(nested, tmplt)
                }
                val = string(stack.Pop().([]byte))
            }

            if len(nested) > 1 {
                reverseSlice(nested)
            }

            if i+1 < len(indices) && string(data[indices[i+1][0]:indices[i+1][1]]) == "{{" {
                if indices[i+1][0] - indices[i][1] > 0 {
                    pseudo := &WiktionaryTemplate{TemplateName: "pseudo", PrettyStr: string(data[indices[i][1]:indices[i+1][0]])}
                    nested = append(nested, pseudo)
                }
            }

            templates = append(templates, nested...)
            nested = nil
        }
    }

    rStr := ""
    for i := 0; i < len(templates); i++ {
        rStr = rStr + templates[i].PrettyStr
    }

    // account for leading and trailing text
    rStr = string(data[:indices[0][0]]) + rStr + string(data[indices[len(indices)-1][1]:])

    return rStr, nil
}

// parameter is the string of the bits between the template open/close tags "{{ ... }}"
func ParseWiktionaryTemplate(tmplt string) *WiktionaryTemplate {
    tokens := strings.Split(tmplt, "|")
    template := &WiktionaryTemplate{}
    template.TemplateName = tokens[0]

    for i := 1; i < len(tokens); i++ {
        token := tokens[i]
        param := ""

        if (strings.Contains(token, "\"=\"") && strings.Contains(token, "=")) || strings.Contains(token, "=") {
            sub_tokens := strings.SplitN(token, "=", 2)
            if stringInSlice(sub_tokens[1], langList) {
                continue
            }
            param = sub_tokens[1]
        } else {
            if stringInSlice(token, langList) || token == "_" {
                continue
            }
            param = token
        }
        template.Params = append(template.Params, param)
    }

    if stringInSlice(template.TemplateName, wikiSubs) {
        template.PrettyStr = fmt.Sprintf(tags[template.TemplateName], strings.Join(template.Params, ", "))
    } else {
        template.PrettyStr = tags[template.TemplateName] + strings.Join(template.Params, ", ")
    }
    return template
}

func (wt *WiktionaryTemplate) ToString() string {
    return fmt.Sprintf("%s %v", wt.TemplateName, wt.Params)
}

func stringInSlice(str string, list []string) bool {
	for _, lStr := range list {
		if str == lStr {
			return true
		}
	}
	return false
}

func reverseSlice(list []*WiktionaryTemplate) {
    for l, r := 0, len(list)-1; l < r; l, r = l+1, r-1 {
        list[l], list[r] = list[r], list[l]
    }
}
