package wikitemplates

import (
    "regexp"
    "strings"
    "fmt"
    "errors"

    "github.com/golang-collections/collections/stack"
)

var (
    wikiTplt  *regexp.Regexp = regexp.MustCompile(`\{\{|\}\}`) // open close template bounds "{{ ... }}"
    NumParam  *regexp.Regexp = regexp.MustCompile(`[0-9]+`)
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
        "surname": "[surname] ",
        "given name": "[given name] ",
        "place": "place: ",
        "en-past of": "past participle of ",
        "present participle of": "present participle of ",
        "synonym of": "synonym of ",
        "initialism of": "initialism of ",
        "nonstandard spelling of": "nonstandard spelling of ",
        "q": "",
        "misspelling of": "misspelling of ",
        "defdate": "",
        "w": "",
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
}

func ParseRecursive(data []byte) (string, error) {
    // get the indices of open/close pairs
    indices := wikiTplt.FindAllIndex(data, -1)

    if len(indices) % 2 != 0 {
        return "", errors.New(fmt.Sprintf("Mismatch tags"))
    }

    fmt.Printf("DATA SIZE: %d\n", len(data))
    fmt.Printf("START OF 1ST INDEX: %d\n", indices[0][0])
    fmt.Printf("END OF LAST INDEX: %d\n", indices[len(indices)-1][1])
    var templates []*WiktionaryTemplate
    stack := stack.New()
    for i := 0; i < len(indices); i++ {
        chunk := data[indices[i][0]:indices[i][1]]
        if string(chunk) == "{{" {
            stack.Push(chunk)
            stack.Push(data[indices[i][1]:indices[i+1][0]])
        }

        if string(chunk) == "}}" {
            val := string(stack.Pop().([]byte))
            for val != "{{" {
                tmplt := ParseWiktionaryTemplate(val)
                val = string(stack.Pop().([]byte))

                templates = append(templates, tmplt)
            }
        }
    }

    rStr := ""
    for i := 0; i < len(templates); i++ {
        rStr = templates[i].PrettyStr + rStr
    }



    return rStr, nil
}

// parameter is the string of the bits between the template open/close tags "{{ ... }}"
func ParseWiktionaryTemplate(tmplt string) *WiktionaryTemplate {
    tokens := strings.Split(tmplt, "|")
    template := &WiktionaryTemplate{}
    template.TemplateName = tokens[0]

    anon_idx := 1
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
            if stringInSlice(token, langList) {
                continue
            }
            param = token
            anon_idx++
        }
        template.Params = append(template.Params, param)
    }

    template.PrettyStr = tags[template.TemplateName] + strings.Join(template.Params, "; ")
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
