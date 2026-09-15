//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"net/url"
	"os"
	"path"
	"strings"
	"text/template"
	"unicode"

	"github.com/pkg/errors"
)

type Rule struct {
	Name         string
	Description  string
	URL          *url.URL
	PageName     string
	URLAlias     string
	Experimental bool
}

const tmplStr = `---
title: {{ .Rule.Name }}
description: {{ .Rule.Description }}
{{- if .Rule.URLAlias }}
aliases:
  - {{ .Rule.URLAlias }}
{{- end }}
---
{{- if .Rule.Experimental }}

> [!NOTE]
> This check is experimental and is not enabled by default. To enable it, see
> [Experimental checks](https://docs.docker.com/go/build-checks-experimental/).
{{- end }}

{{ .Content }}
`

var destDir string

func main() {
	if len(os.Args) < 2 {
		panic("Please provide a destination directory")
	}
	destDir = os.Args[1]
	log.Printf("Destination directory: %s\n", destDir)
	if err := run(destDir); err != nil {
		panic(err)
	}
}

func run(destDir string) error {
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return err
	}
	rules, err := listRules()
	if err != nil {
		return err
	}

	tmplRule, err := template.New("rule").Parse(tmplStr)
	if err != nil {
		return err
	}
	for _, rule := range rules {
		if ok, err := genRuleDoc(rule, tmplRule); err != nil {
			return errors.Wrapf(err, "Error generating docs for %s", rule.Name)
		} else if ok {
			log.Printf("Docs generated for %s\n", rule.Name)
		}
	}

	return genIndex(rules)
}

func genRuleDoc(rule Rule, tmpl *template.Template) (bool, error) {
	mdfilename := fmt.Sprintf("docs/%s.md", rule.Name)
	content, err := os.ReadFile(mdfilename)
	if err != nil {
		return false, err
	}
	outputfile, err := os.Create(path.Join(destDir, rule.PageName+".md"))
	if err != nil {
		return false, err
	}
	defer outputfile.Close()
	if err = tmpl.Execute(outputfile, struct {
		Rule    Rule
		Content string
	}{
		Rule:    rule,
		Content: string(content),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func genIndex(rules []Rule) error {
	content, err := os.ReadFile("docs/_index.md")
	if err != nil {
		return err
	}

	tmpl, err := template.New("index").Parse(string(content))
	if err != nil {
		return err
	}

	outputfile, err := os.Create(path.Join(destDir, "_index.md"))
	if err != nil {
		return err
	}
	defer outputfile.Close()

	return tmpl.Execute(outputfile, struct {
		Rules []Rule
	}{
		Rules: rules,
	})
}

func listRules() ([]Rule, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "ruleset.go", nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var rules []Rule
	var inspectErr error
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			for _, spec := range x.Specs {
				if vSpec, ok := spec.(*ast.ValueSpec); ok {
					rule := Rule{}
					if cl, ok := vSpec.Values[0].(*ast.CompositeLit); ok {
						for _, elt := range cl.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok {
								switch kv.Key.(*ast.Ident).Name {
								case "Name":
									if basicLit, ok := kv.Value.(*ast.BasicLit); ok {
										rule.Name = strings.Trim(basicLit.Value, `"`)
										rule.PageName = camelToKebab(rule.Name)
									}
								case "Description":
									if basicLit, ok := kv.Value.(*ast.BasicLit); ok {
										rule.Description = strings.Trim(basicLit.Value, `"`)
									}
								case "URL":
									if basicLit, ok := kv.Value.(*ast.BasicLit); ok {
										ruleURL := strings.Trim(basicLit.Value, `"`)
										u, err := url.Parse(ruleURL)
										if err != nil {
											inspectErr = errors.Wrapf(err, "cannot parse URL %s", ruleURL)
											return false
										}
										rule.URL = u
									}
								case "Experimental":
									if basicLit, ok := kv.Value.(*ast.Ident); ok {
										rule.Experimental = basicLit.Name == "true"
									}
								}
							}
						}
					}
					if rule.Name == "InvalidBaseImagePlatform" {
						// this rule does not have any specific documentation needed
						continue
					}
					if rule.URL == nil {
						inspectErr = errors.Errorf("URL not set for %q", rule.Name)
						return false
					}
					if strings.HasPrefix(rule.URL.String(), `https://docs.docker.com/go/`) {
						lastEntry := path.Base(rule.URL.Path)
						if lastEntry != camelToKebab(rule.Name) {
							inspectErr = errors.Errorf("Last entry %q in URL is malformed, should be: %q", lastEntry, camelToKebab(rule.Name))
							return false
						}
						rule.URLAlias = strings.TrimPrefix(rule.URL.String(), `https://docs.docker.com`)
					}
					rules = append(rules, rule)
				}
			}
		}
		return true
	})
	if inspectErr != nil {
		return nil, inspectErr
	}
	return rules, nil
}

func camelToKebab(s string) string {
	var res []rune
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i != 0 && (unicode.IsLower(rune(s[i-1])) || (i+1 < len(s) && unicode.IsLower(rune(s[i+1])))) {
				res = append(res, '-')
			}
			res = append(res, unicode.ToLower(r))
		} else {
			res = append(res, r)
		}
	}
	return string(res)
}
